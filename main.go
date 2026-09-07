package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"io"
	"log"
	"net/mail"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/go-resty/resty/v2"
)

// One client for the process, not one per message: resty.New() per delivery
// threw away connection reuse to the webhook.
var webhookClient = resty.New()

func main() {
	// not init(): flag.Parse() there also eats `go test` flags and breaks the tests
	flag.Parse()

	if *flagAuthUSER != "" || *flagAuthPASS != "" {
		log.Println("warning: -user/-pass are accepted for compatibility but ignored; " +
			"this server does not authenticate senders. Restrict access with -domain and your firewall.")
	}

	srv := smtp.NewServer(&backend{handle: deliver})
	srv.Addr = *flagListenAddr
	srv.Domain = *flagServerName
	srv.ReadTimeout = time.Duration(*flagReadTimeout) * time.Second
	srv.WriteTimeout = time.Duration(*flagWriteTimeout) * time.Second
	srv.MaxMessageBytes = *flagMaxMessageSize
	srv.AllowInsecureAuth = true
	srv.EnableSMTPUTF8 = false

	log.Fatal(listenAndServe(srv))
}

// deliver POSTs one message to the webhook. Any error fails the SMTP
// transaction so the sender retries instead of the mail being lost.
func deliver(env *envelope) error {
	if !recipientAllowed(env.to.Address, *flagDomain) {
		log.Printf("rejected %q: TO domain not allowed", env.to.Address)
		return errors.New("Unauthorized TO domain")
	}

	resp, err := webhookClient.R().
		SetHeader("Content-Type", "application/json").
		SetBody(buildPayload(env)).
		Post(*flagWebhook)
	if err != nil {
		log.Println(err)
		return errors.New("E1: Cannot accept your message due to internal error, please report that to our engineers")
	} else if resp.StatusCode() != 200 {
		log.Println(resp.Status())
		return errors.New("E2: Cannot accept your message due to internal error, please report that to our engineers")
	}

	return nil
}

// buildPayload maps a parsed message onto the JSON sent to the webhook. The
// shape is a public contract -- testdata/golden holds the payloads 1.0.0
// produced, and parser_test.go checks this still matches them.
func buildPayload(env *envelope) *EmailMessage {
	msg := env.message

	out := EmailMessage{
		ID:            msg.MessageID,
		Date:          msg.Date.String(),
		References:    msg.References,
		SPFResult:     string(env.spf),
		ResentDate:    msg.ResentDate.String(),
		ResentID:      msg.ResentMessageID,
		Subject:       msg.Subject,
		Attachments:   []*EmailAttachment{},
		EmbeddedFiles: []*EmailEmbeddedFile{},
	}

	out.Body.HTML = string(msg.HTMLBody)
	out.Body.Text = string(msg.TextBody)

	out.Addresses.From = transformStdAddressToEmailAddress([]*mail.Address{env.from})[0]
	out.Addresses.To = transformStdAddressToEmailAddress([]*mail.Address{env.to})[0]
	out.Addresses.Cc = transformStdAddressToEmailAddress(msg.Cc)
	out.Addresses.Bcc = transformStdAddressToEmailAddress(msg.Bcc)
	out.Addresses.ReplyTo = transformStdAddressToEmailAddress(msg.ReplyTo)
	out.Addresses.InReplyTo = msg.InReplyTo

	if resentFrom := transformStdAddressToEmailAddress(msg.ResentFrom); len(resentFrom) > 0 {
		out.Addresses.ResentFrom = resentFrom[0]
	}

	out.Addresses.ResentTo = transformStdAddressToEmailAddress(msg.ResentTo)
	out.Addresses.ResentCc = transformStdAddressToEmailAddress(msg.ResentCc)
	out.Addresses.ResentBcc = transformStdAddressToEmailAddress(msg.ResentBcc)

	for _, a := range msg.Attachments {
		data, _ := io.ReadAll(a.Data)
		out.Attachments = append(out.Attachments, &EmailAttachment{
			Filename:    a.Filename,
			ContentType: a.ContentType,
			Data:        base64.StdEncoding.EncodeToString(data),
		})
	}

	for _, a := range msg.EmbeddedFiles {
		data, _ := io.ReadAll(a.Data)
		out.EmbeddedFiles = append(out.EmbeddedFiles, &EmailEmbeddedFile{
			CID:         a.CID,
			ContentType: a.ContentType,
			Data:        base64.StdEncoding.EncodeToString(data),
		})
	}

	return &out
}
