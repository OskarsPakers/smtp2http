package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/mail"
	"strings"
	"time"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-smtp"
)

// handler processes one fully received message. Returning an error fails the
// SMTP transaction, which tells the sending server to retry rather than drop
// the mail on the floor.
type handler func(env *envelope) error

// envelope is what the handler needs: the SMTP-level addresses (which are not
// necessarily the From/To headers), the parsed message, and the SPF verdict.
type envelope struct {
	from    *mail.Address
	to      *mail.Address
	message *Email
	spf     spf.Result
}

type backend struct {
	handle handler
}

func (b *backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	var remote net.IP
	if addr, ok := c.Conn().RemoteAddr().(*net.TCPAddr); ok {
		remote = addr.IP
	}

	return &session{handle: b.handle, remote: remote}, nil
}

type session struct {
	handle handler
	remote net.IP
	from   string
	to     string
}

// spfTimeout bounds the DNS work done for a sender-chosen domain. Without it
// the sender's own name servers decide how long the session is held.
const spfTimeout = 5 * time.Second

var (
	errBadAddress = &smtp.SMTPError{
		Code:         553,
		EnhancedCode: smtp.EnhancedCode{5, 1, 3},
		Message:      "Malformed mailbox address",
	}
	errNullSender = &smtp.SMTPError{
		Code:         550,
		EnhancedCode: smtp.EnhancedCode{5, 7, 1},
		Message:      "Mail with an empty sender is not accepted",
	}
	errBadSequence = &smtp.SMTPError{
		Code:         503,
		EnhancedCode: smtp.EnhancedCode{5, 5, 1},
		Message:      "MAIL and RCPT must come before the message",
	}
	// Temporary: a transfer that stalled or broke off may well succeed on a retry.
	errReadFailed = &smtp.SMTPError{
		Code:         451,
		EnhancedCode: smtp.EnhancedCode{4, 4, 2},
		Message:      "Connection problem while reading your message",
	}
)

// validEnvelopeAddress accepts only a plain ASCII local@domain mailbox.
//
// go-smtp hands over whatever stood between the angle brackets. Anything
// net/mail would normalise -- a comment, a display name, quoting -- is refused,
// because the SPF check, the -domain filter and the payload must all see the
// same address: "<ceo@bank.example()>" used to be reported as ceo@bank.example
// with an SPF verdict computed for a different string.
func validEnvelopeAddress(addr string) bool {
	for i := 0; i < len(addr); i++ {
		if addr[i] >= 0x80 {
			return false // SMTPUTF8 is off
		}
	}

	parsed, err := mail.ParseAddress(addr)

	return err == nil && parsed.Name == "" && parsed.Address == addr
}

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	if from == "" {
		return errNullSender
	}

	if !validEnvelopeAddress(from) {
		return errBadAddress
	}

	s.from = from

	return nil
}

// Rcpt applies the -domain filter here rather than after DATA, so mail for
// other domains costs no parsing and no SPF lookups. deliver checks again.
func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if !validEnvelopeAddress(to) {
		return errBadAddress
	}

	if !recipientAllowed(to, *flagDomain) {
		slog.Warn("rejected", "from", s.from, "to", to,
			"reason", "to_domain_not_allowed", "domain_filter", domainFilter())

		return errDomainNotAllowed
	}

	s.to = to

	return nil
}

func (s *session) Data(r io.Reader) error {
	// Snapshot first: with BDAT go-smtp runs Data on a goroutine of its own
	// while RSET, MAIL and RCPT keep rewriting the session fields.
	from, to := s.from, s.to

	msg, parseErr := ParseEmail(r)

	// The parser may stop before the end of the message. Always read to the
	// end: only then is it known that the client committed the message (BDAT
	// LAST, or the final dot) rather than aborting it, and returning while
	// go-smtp is still feeding chunks into the reader wedges the connection.
	_, readErr := io.Copy(io.Discard, r)

	if err := dataError(parseErr, readErr); err != nil {
		cause := readErr
		if cause == nil {
			cause = parseErr
		}

		slog.Warn("rejected", "from", from, "to", to, "reason", "unreadable_message", "err", cause)

		return err
	}

	if from == "" || to == "" {
		return errBadSequence
	}

	return s.handle(&envelope{
		from:    &mail.Address{Address: from},
		to:      &mail.Address{Address: to},
		message: msg,
		spf:     checkSPF(s.remote, from),
	})
}

// dataError maps a failed transfer or parse onto the SMTP reply. Transport
// errors get a fixed text: their message names the socket addresses, which is
// nobody's business but ours.
func dataError(parseErr, readErr error) error {
	var smtpErr *smtp.SMTPError
	var netErr net.Error

	switch {
	case readErr != nil && errors.As(readErr, &smtpErr):
		return smtpErr // e.g. 552, message too large
	case readErr != nil:
		return errReadFailed
	case parseErr == nil:
		return nil
	case errors.As(parseErr, &smtpErr):
		return smtpErr
	case errors.As(parseErr, &netErr):
		return errReadFailed
	}

	// Permanent: the same bytes will not parse on a retry either.
	return &smtp.SMTPError{
		Code:         554,
		EnhancedCode: smtp.EnhancedCode{5, 6, 0},
		Message:      "Cannot read your message: " + parseErr.Error(),
	}
}

// checkSPF mirrors what go-smtpsrv did: evaluate the envelope sender against
// the connecting IP, and treat any failure to evaluate as "none" rather than
// rejecting the message.
func checkSPF(remote net.IP, from string) spf.Result {
	if remote == nil || from == "" {
		return spf.None
	}

	at := strings.LastIndex(from, "@")
	if at < 0 {
		return spf.None
	}

	ctx, cancel := context.WithTimeout(context.Background(), spfTimeout)
	defer cancel()

	result, _ := spf.CheckHostWithSender(remote, from[at+1:], from, spf.WithContext(ctx))

	return result
}

func (s *session) Reset() { s.from, s.to = "", "" }

func (s *session) Logout() error { return nil }

// listenAndServe binds first so the log line can report the address the kernel
// actually gave us. go-smtpsrv printed the raw config string, so the default
// ":smtp" was reported verbatim and never as port 25.
func listenAndServe(cfg *smtp.Server) error {
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}

	slog.Info("listening", "addr", ln.Addr().String())

	return cfg.Serve(ln)
}
