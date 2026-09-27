package smtpmail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/wneessen/go-mail"
)

// Kept below the HTTP server WriteTimeout (30s) so API callers get a real error.
const operationTimeout = 25 * time.Second

type Credentials struct {
	Host     string
	Port     int
	UseTLS   bool
	Username string
	Password string
	From     string
}

type SendParams struct {
	To         []string
	CC         []string
	BCC        []string
	Subject    string
	BodyText   string
	BodyHTML   string
	InReplyTo  string
	References string
}

// SentMessage is the message as delivered, so callers can file a copy in Sent.
type SentMessage struct {
	MessageID string
	Raw       []byte
}

type Client struct{}

func NewClient() *Client { return &Client{} }

func (c *Client) Test(ctx context.Context, creds Credentials) error {
	client, err := newMailClient(creds)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := client.DialWithContext(ctx); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return client.Close()
}

func (c *Client) Send(ctx context.Context, creds Credentials, params SendParams) (SentMessage, error) {
	msg, err := buildMessage(creds.From, params)
	if err != nil {
		return SentMessage{}, err
	}
	client, err := newMailClient(creds)
	if err != nil {
		return SentMessage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	if err := client.DialAndSendWithContext(ctx, msg); err != nil {
		return SentMessage{}, fmt.Errorf("smtp: %w", err)
	}
	var raw bytes.Buffer
	if _, err := msg.WriteTo(&raw); err != nil {
		return SentMessage{MessageID: msg.GetMessageID()}, nil
	}
	return SentMessage{MessageID: msg.GetMessageID(), Raw: raw.Bytes()}, nil
}

func buildMessage(from string, params SendParams) (*mail.Msg, error) {
	if len(params.To) == 0 {
		return nil, fmt.Errorf("at least one recipient is required")
	}
	msg := mail.NewMsg()
	if err := msg.From(from); err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	if err := msg.To(params.To...); err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	if len(params.CC) > 0 {
		if err := msg.Cc(params.CC...); err != nil {
			return nil, fmt.Errorf("cc: %w", err)
		}
	}
	if len(params.BCC) > 0 {
		if err := msg.Bcc(params.BCC...); err != nil {
			return nil, fmt.Errorf("bcc: %w", err)
		}
	}
	msg.Subject(params.Subject)
	msg.SetMessageID()
	msg.SetDate()
	if params.InReplyTo != "" {
		msg.SetGenHeader(mail.HeaderInReplyTo, params.InReplyTo)
	}
	if params.References != "" {
		msg.SetGenHeader(mail.HeaderReferences, params.References)
	}

	bodyText := strings.TrimSpace(params.BodyText)
	bodyHTML := strings.TrimSpace(params.BodyHTML)
	switch {
	case bodyHTML != "" && bodyText != "":
		msg.SetBodyString(mail.TypeTextPlain, bodyText)
		msg.AddAlternativeString(mail.TypeTextHTML, bodyHTML)
	case bodyHTML != "":
		msg.SetBodyString(mail.TypeTextHTML, bodyHTML)
	default:
		msg.SetBodyString(mail.TypeTextPlain, bodyText)
	}
	return msg, nil
}

func newMailClient(creds Credentials) (*mail.Client, error) {
	opts := []mail.Option{
		mail.WithPort(creds.Port),
		mail.WithUsername(creds.Username),
		mail.WithPassword(creds.Password),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		mail.WithTimeout(operationTimeout),
		mail.WithTLSConfig(&tls.Config{ServerName: creds.Host, MinVersion: tls.VersionTLS12}),
	}
	if creds.UseTLS {
		if creds.Port == 465 {
			opts = append(opts, mail.WithSSL())
		} else {
			opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
		}
	} else {
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	}
	client, err := mail.NewClient(creds.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("smtp: %w", err)
	}
	return client, nil
}
