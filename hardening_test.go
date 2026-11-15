package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"net/textproto"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gosmtp "github.com/emersion/go-smtp"
)

// countingHook answers 200 and counts the requests it saw.
func countingHook(n *atomic.Int32) http.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) { n.Add(1) }
}

func wantCode(t *testing.T, err error, code int) {
	t.Helper()

	tperr, ok := err.(*textproto.Error)
	if !ok || tperr.Code != code {
		t.Errorf("reply = %v, want %d", err, code)
	}
}

// A redirecting webhook must not be followed: the target would receive the
// webhook URL in the Referer header, and its 200 would acknowledge the mail.
func TestWebhookRedirectIsNotFollowed(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var elsewhere atomic.Int32

			other := httptest.NewServer(countingHook(&elsewhere))
			defer other.Close()

			addr := startServer(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, other.URL+"/elsewhere", status)
			}, "")

			wantCode(t, send(t, addr, "bob@example.com", plainMessage), 451)

			if n := elsewhere.Load(); n != 0 {
				t.Errorf("redirect target received %d requests, want none", n)
			}
		})
	}
}

// The SPF verdict and the payload must describe the same address, so anything
// net/mail would tidy up is refused before it gets that far.
func TestEnvelopeAddressesAreStrict(t *testing.T) {
	var posts atomic.Int32

	addr := startServer(t, countingHook(&posts), "")

	bad := []string{
		"ceo@bank.example()",
		"ceo@bank.example(@attacker.example)",
		`"CEO <ceo@bank.example> ("@attacker.example)`,
		`"x@y"@bank.example`,
		"bob@Kiosk.test",
	}

	for _, from := range bad {
		c, err := smtp.Dial(addr)
		if err != nil {
			t.Fatal(err)
		}

		wantCode(t, c.Mail(from), 553)
		_ = c.Close()
	}

	for _, to := range bad {
		c, err := smtp.Dial(addr)
		if err != nil {
			t.Fatal(err)
		}

		if err := c.Mail("alice@sender.invalid"); err != nil {
			t.Fatal(err)
		}

		wantCode(t, c.Rcpt(to), 553)
		_ = c.Close()
	}

	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	wantCode(t, c.Mail(""), 550)

	if n := posts.Load(); n != 0 {
		t.Errorf("webhook received %d requests, want none", n)
	}
}

// A recipient outside -domain is refused at RCPT, before the message is sent.
func TestDomainFilterAnswersAtRcpt(t *testing.T) {
	var posts atomic.Int32

	addr := startServer(t, countingHook(&posts), "example.com")

	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Mail("alice@sender.invalid"); err != nil {
		t.Fatal(err)
	}

	wantCode(t, c.Rcpt("bob@other.tld"), 550)

	if err := c.Rcpt("bob@EXAMPLE.com"); err != nil {
		t.Errorf("allowed recipient refused: %v", err)
	}
}

// A message cut short by the size limit must be refused, not delivered
// truncated. text/plain without a Content-Transfer-Encoding used to slip through.
func TestOversizeMessageIsNotDeliveredTruncated(t *testing.T) {
	for _, cte := range []string{"", "Content-Transfer-Encoding: 8bit\r\n"} {
		var posts atomic.Int32

		addr := startServer(t, countingHook(&posts), "", func(s *gosmtp.Server) { s.MaxMessageBytes = 600 })

		raw := "From: a@sender.invalid\r\nSubject: s\r\n" + cte + "\r\n" + strings.Repeat(strings.Repeat("x", 62)+"\r\n", 30)

		wantCode(t, send(t, addr, "bob@example.com", raw), 552)

		if n := posts.Load(); n != 0 {
			t.Errorf("cte %q: webhook received %d requests, want none", cte, n)
		}
	}
}

// A stalled transfer is answered without the socket addresses the raw error carries.
func TestStalledDataReplyHidesAddresses(t *testing.T) {
	addr := startServer(t, func(http.ResponseWriter, *http.Request) {}, "",
		func(s *gosmtp.Server) { s.ReadTimeout = 300 * time.Millisecond })

	c := dialRaw(t, addr)
	c.cmd(t, 250, "MAIL FROM:<alice@sender.invalid>")
	c.cmd(t, 250, "RCPT TO:<bob@example.com>")
	c.cmd(t, 354, "DATA")

	if err := c.PrintfLine("From: a@sender.invalid\r\nSubject: s\r\nX-Partial: abc"); err != nil {
		t.Fatal(err)
	}

	code, msg, _ := c.ReadResponse(0)
	if code != 451 || strings.Contains(msg, "127.0.0.1") {
		t.Errorf("reply = %d %q, want a 451 without addresses", code, msg)
	}
}

// A multipart message whose closing boundary is followed by more data: the
// parser is done at the boundary, long before the transfer is.
const closedMultipart = "From: alice@sender.invalid\r\nTo: bob@example.com\r\nSubject: report\r\n" +
	"Content-Type: multipart/mixed; boundary=mb\r\n\r\n--mb\r\nContent-Type: text/plain\r\n\r\nbody\r\n--mb--\r\n"

var trailingMessage = closedMultipart + strings.Repeat(strings.Repeat("e", 70)+"\r\n", 230)

type rawConn struct{ *textproto.Conn }

func dialRaw(t *testing.T, addr string) rawConn {
	t.Helper()

	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}

	_ = nc.SetDeadline(time.Now().Add(10 * time.Second))

	c := rawConn{textproto.NewConn(nc)}
	t.Cleanup(func() { _ = c.Close() })

	if _, _, err := c.ReadResponse(220); err != nil {
		t.Fatal(err)
	}

	c.cmd(t, 250, "EHLO client.invalid")

	return c
}

func (c rawConn) cmd(t *testing.T, want int, format string, args ...any) {
	t.Helper()

	if err := c.PrintfLine(format, args...); err != nil {
		t.Fatal(err)
	}

	if _, _, err := c.ReadResponse(want); err != nil {
		t.Fatalf("%s: %v", fmt.Sprintf(format, args...), err)
	}
}

// bdat sends one chunk without waiting for the reply.
func (c rawConn) bdat(t *testing.T, chunk string, last bool) {
	t.Helper()

	suffix := ""
	if last {
		suffix = " LAST"
	}

	if _, err := fmt.Fprintf(c.W, "BDAT %d%s\r\n%s", len(chunk), suffix, chunk); err != nil {
		t.Fatal(err)
	}

	if err := c.W.Flush(); err != nil {
		t.Fatal(err)
	}
}

// BDAT must behave like DATA: one delivery, after the client committed the
// message, and a reply that matches the webhook's answer.
func TestBDATMatchesDATA(t *testing.T) {
	var posts atomic.Int32

	addr := startServer(t, countingHook(&posts), "")

	if err := send(t, addr, "bob@example.com", trailingMessage); err != nil {
		t.Fatalf("DATA: %v", err)
	}

	c := dialRaw(t, addr)
	c.cmd(t, 250, "MAIL FROM:<alice@sender.invalid>")
	c.cmd(t, 250, "RCPT TO:<bob@example.com>")
	c.bdat(t, trailingMessage, true)

	if _, _, err := c.ReadResponse(250); err != nil {
		t.Errorf("BDAT LAST: %v", err)
	}

	if n := posts.Load(); n != 2 {
		t.Errorf("webhook received %d requests, want 2 (one per transfer)", n)
	}
}

// A transaction the client aborts must deliver nothing, and the connection
// must stay usable for the next one, which is delivered under its own envelope.
func TestBDATAbortDeliversNothing(t *testing.T) {
	var posts atomic.Int32

	seen := make(chan string, 4)

	addr := startServer(t, func(_ http.ResponseWriter, r *http.Request) {
		posts.Add(1)

		body, _ := io.ReadAll(r.Body)
		seen <- string(body)
	}, "")

	c := dialRaw(t, addr)
	c.cmd(t, 250, "MAIL FROM:<alice@sender.invalid>")
	c.cmd(t, 250, "RCPT TO:<bob@example.com>")
	c.bdat(t, closedMultipart, false)

	if _, _, err := c.ReadResponse(250); err != nil {
		t.Fatalf("first chunk: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	if n := posts.Load(); n != 0 {
		t.Fatalf("webhook received %d requests before BDAT LAST", n)
	}

	c.cmd(t, 250, "RSET")

	// The next transaction uses DATA: go-smtp 0.26 swaps its per-connection
	// result channel without synchronisation when a second BDAT transfer
	// follows an aborted one, which the race detector reports.
	c.cmd(t, 250, "MAIL FROM:<second@other.invalid>")
	c.cmd(t, 250, "RCPT TO:<carol@example.org>")
	c.cmd(t, 354, "DATA")

	if _, err := io.WriteString(c.W, plainMessage+".\r\n"); err != nil {
		t.Fatal(err)
	}

	if err := c.W.Flush(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := c.ReadResponse(250); err != nil {
		t.Fatalf("second transaction: %v", err)
	}

	if n := posts.Load(); n != 1 {
		t.Fatalf("webhook received %d requests, want 1", n)
	}

	if body := <-seen; !strings.Contains(body, "second@other.invalid") || !strings.Contains(body, "carol@example.org") ||
		!strings.Contains(body, "Dobr") {
		t.Errorf("delivered payload does not belong to the second transaction: %s", body)
	}
}

// Parsing must stay linear in the message size however the body is cut up.
func TestManyTextPartsParseLinearly(t *testing.T) {
	var b strings.Builder

	b.WriteString("Subject: x\r\nContent-Type: multipart/alternative; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\n")
	b.WriteString(strings.Repeat(strings.Repeat("a", 74)+"\r\n", 3400))

	for i := 0; i < 7000; i++ {
		b.WriteString("\r\n--b\r\nContent-Type:text/plain\r\n\r\nx")
	}

	b.WriteString("\r\n--b--\r\n")
	raw := b.String()

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)

	if _, err := ParseEmail(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}

	runtime.ReadMemStats(&after)

	// The quadratic version allocated about 4000 times the message size here.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 100*uint64(len(raw)) {
		t.Errorf("parsing %d bytes allocated %d bytes", len(raw), allocated)
	}
}

// Parts are kept as plain bytes: a retained charset decoder costs 8 KiB each.
func TestPartsHoldBytesNotDecoders(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--b\r\nContent-Type: application/octet-stream; charset=utf-8\r\nContent-Disposition: attachment; filename=nocte.bin\r\n\r\nPAYLOAD\r\n" +
		"--b\r\nContent-Type: multipart/related; boundary=r\r\n\r\n" +
		"--r\r\nContent-Type: image/png; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\nContent-Id: <c1>\r\n\r\nPNG\r\n--r--\r\n" +
		"--b--\r\n"

	email, err := ParseEmail(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}

	if len(email.Attachments) != 1 || len(email.EmbeddedFiles) != 1 {
		t.Fatalf("got %d attachments and %d embedded files", len(email.Attachments), len(email.EmbeddedFiles))
	}

	if _, ok := email.Attachments[0].Data.(*bytes.Reader); !ok {
		t.Errorf("attachment data is a %T", email.Attachments[0].Data)
	}

	if _, ok := email.EmbeddedFiles[0].Data.(*bytes.Reader); !ok {
		t.Errorf("embedded file data is a %T", email.EmbeddedFiles[0].Data)
	}

	// Without a Content-Transfer-Encoding the data used to arrive empty.
	if data, _ := io.ReadAll(email.Attachments[0].Data); string(data) != "PAYLOAD" {
		t.Errorf("attachment data = %q", data)
	}
}

func TestBaseFilename(t *testing.T) {
	cases := map[string]string{
		"report.pdf":         "report.pdf",
		"../../enc/q.txt":    "q.txt",
		"/absolute/b.txt":    "b.txt",
		`..\..\win\c.txt`:    "c.txt",
		"a\x00b\r\nc.txt":    "abc.txt",
		"..":                 "",
		"dir/":               "",
		"Zpráva o stavu.pdf": "Zpráva o stavu.pdf",
	}

	for in, want := range cases {
		if got := baseFilename(in); got != want {
			t.Errorf("baseFilename(%q) = %q, want %q", in, got, want)
		}
	}

	raw := "Subject: x\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=\"=?utf-8?q?..=2F..=2Fenc=2Fq.txt?=\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\naGk=\r\n--b--\r\n"

	// text/plain parts go to the body, so use a type that is an attachment.
	raw = strings.Replace(raw, "Content-Type: text/plain\r\nContent-Disposition", "Content-Type: application/pdf\r\nContent-Disposition", 1)

	email, err := ParseEmail(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}

	if len(email.Attachments) != 1 || email.Attachments[0].Filename != "q.txt" {
		t.Errorf("attachments = %+v", email.Attachments)
	}
}
