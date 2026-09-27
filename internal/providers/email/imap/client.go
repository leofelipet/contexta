package imapmail

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

const (
	// Kept below the HTTP server WriteTimeout (30s) so API callers get a real error.
	operationTimeout = 25 * time.Second
	maxBodyBytes     = 100 << 10
	maxFetchBytes    = 2 << 20
	defaultLimit     = 20
	maxLimit         = 50
)

var (
	ErrMessageNotFound = errors.New("message not found")
	ErrUnsafeExpunge   = errors.New("server supports neither MOVE nor UIDPLUS; refusing to expunge other messages in the folder")
	ErrNoSentMailbox   = errors.New("no Sent mailbox found")
)

type Credentials struct {
	Host     string
	Port     int
	UseTLS   bool
	Username string
	Password string
}

type Mailbox struct {
	Name       string
	Delimiter  string
	Attributes []string
}

type Address struct {
	Name    string
	Address string
}

type AttachmentMeta struct {
	Filename string
	MIMEType string
	Size     int
}

type Summary struct {
	UID     uint32
	Folder  string
	Subject string
	From    []Address
	To      []Address
	Date    time.Time
	Flags   []string
	Size    int
}

type Message struct {
	Summary
	CC            []Address
	BCC           []Address
	ReplyTo       []Address
	MessageID     string
	InReplyTo     string
	BodyText      string
	BodyHTML      string
	BodyTruncated bool
	Attachments   []AttachmentMeta
}

type SearchParams struct {
	Folder  string
	Unseen  bool
	From    string
	To      string
	Subject string
	Since   *time.Time
	Before  *time.Time
	Limit   int
}

type FlagUpdate struct {
	Add    []string
	Remove []string
}

type Client struct{}

func NewClient() *Client { return &Client{} }

func (c *Client) Test(ctx context.Context, creds Credentials) error {
	return withSession(ctx, creds, func(*imapclient.Client) error { return nil })
}

func (c *Client) ListMailboxes(ctx context.Context, creds Credentials) ([]Mailbox, error) {
	var out []Mailbox
	err := withSession(ctx, creds, func(client *imapclient.Client) error {
		list, err := client.List("", "*", nil).Collect()
		if err != nil {
			return fmt.Errorf("list mailboxes: %w", err)
		}
		out = make([]Mailbox, 0, len(list))
		for _, m := range list {
			attrs := make([]string, 0, len(m.Attrs))
			for _, a := range m.Attrs {
				attrs = append(attrs, string(a))
			}
			out = append(out, Mailbox{Name: m.Mailbox, Delimiter: delimiterString(m.Delim), Attributes: attrs})
		}
		return nil
	})
	return out, err
}

func (c *Client) Search(ctx context.Context, creds Credentials, params SearchParams) ([]Summary, error) {
	folder := defaultFolder(params.Folder)
	limit := params.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	out := []Summary{}
	err := withSession(ctx, creds, func(client *imapclient.Client) error {
		if _, err := client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return fmt.Errorf("select mailbox %q: %w", folder, err)
		}
		data, err := client.UIDSearch(buildCriteria(params), nil).Wait()
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		uids := data.AllUIDs()
		if len(uids) == 0 {
			return nil
		}
		slices.Sort(uids)
		if len(uids) > limit {
			uids = uids[len(uids)-limit:]
		}
		messages, err := client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
			UID: true, Flags: true, Envelope: true, RFC822Size: true, InternalDate: true,
		}).Collect()
		if err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
		for _, msg := range messages {
			out = append(out, summaryFromBuffer(folder, msg))
		}
		slices.SortFunc(out, func(a, b Summary) int { return cmp.Compare(b.UID, a.UID) })
		return nil
	})
	return out, err
}

func (c *Client) Get(ctx context.Context, creds Credentials, folder string, uid uint32) (Message, error) {
	folder = defaultFolder(folder)
	if uid == 0 {
		return Message{}, fmt.Errorf("uid is required")
	}
	var result Message
	err := withSession(ctx, creds, func(client *imapclient.Client) error {
		if _, err := client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
			return fmt.Errorf("select mailbox %q: %w", folder, err)
		}
		bodySection := &imap.FetchItemBodySection{Peek: true, Partial: &imap.SectionPartial{Offset: 0, Size: maxFetchBytes}}
		messages, err := client.Fetch(imap.UIDSetNum(imap.UID(uid)), &imap.FetchOptions{
			UID: true, Flags: true, Envelope: true, RFC822Size: true, InternalDate: true,
			BodySection: []*imap.FetchItemBodySection{bodySection},
		}).Collect()
		if err != nil {
			return fmt.Errorf("fetch: %w", err)
		}
		if len(messages) == 0 {
			return ErrMessageNotFound
		}
		msg := messages[0]
		result = Message{Summary: summaryFromBuffer(folder, msg)}
		if msg.Envelope != nil {
			result.CC = mapAddresses(msg.Envelope.Cc)
			result.BCC = mapAddresses(msg.Envelope.Bcc)
			result.ReplyTo = mapAddresses(msg.Envelope.ReplyTo)
			result.MessageID = msg.Envelope.MessageID
			if len(msg.Envelope.InReplyTo) > 0 {
				result.InReplyTo = msg.Envelope.InReplyTo[0]
			}
		}
		if raw := msg.FindBodySection(bodySection); len(raw) > 0 {
			text, html, truncated, attachments := parseMIME(raw)
			result.BodyText = text
			result.BodyHTML = html
			result.BodyTruncated = truncated || msg.RFC822Size > maxFetchBytes
			result.Attachments = attachments
		}
		return nil
	})
	return result, err
}

func (c *Client) SetFlags(ctx context.Context, creds Credentials, folder string, uids []uint32, update FlagUpdate) error {
	folder = defaultFolder(folder)
	if len(uids) == 0 {
		return fmt.Errorf("at least one uid is required")
	}
	return withSession(ctx, creds, func(client *imapclient.Client) error {
		if _, err := client.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select mailbox %q: %w", folder, err)
		}
		uidSet := toUIDSet(uids)
		if flags := toIMAPFlags(update.Add); len(flags) > 0 {
			if err := client.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: flags, Silent: true}, nil).Close(); err != nil {
				return fmt.Errorf("add flags: %w", err)
			}
		}
		if flags := toIMAPFlags(update.Remove); len(flags) > 0 {
			if err := client.Store(uidSet, &imap.StoreFlags{Op: imap.StoreFlagsDel, Flags: flags, Silent: true}, nil).Close(); err != nil {
				return fmt.Errorf("remove flags: %w", err)
			}
		}
		return nil
	})
}

func (c *Client) Move(ctx context.Context, creds Credentials, folder string, uid uint32, destMailbox string) error {
	folder = defaultFolder(folder)
	destMailbox = strings.TrimSpace(destMailbox)
	if destMailbox == "" {
		return fmt.Errorf("destination mailbox is required")
	}
	return withSession(ctx, creds, func(client *imapclient.Client) error {
		if _, err := client.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select mailbox %q: %w", folder, err)
		}
		return moveOne(client, uid, destMailbox)
	})
}

func (c *Client) Delete(ctx context.Context, creds Credentials, folder string, uid uint32) error {
	folder = defaultFolder(folder)
	return withSession(ctx, creds, func(client *imapclient.Client) error {
		trash := findSpecialMailbox(client, imap.MailboxAttrTrash, "Trash", "INBOX.Trash", "Lixeira", "Deleted Items", "Itens Excluídos")
		if _, err := client.Select(folder, nil).Wait(); err != nil {
			return fmt.Errorf("select mailbox %q: %w", folder, err)
		}
		if trash != "" && !strings.EqualFold(trash, folder) {
			return moveOne(client, uid, trash)
		}
		if !client.Caps().Has(imap.CapUIDPlus) {
			return ErrUnsafeExpunge
		}
		uidSet := toUIDSet([]uint32{uid})
		if err := client.Store(uidSet, &imap.StoreFlags{
			Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}, Silent: true,
		}, nil).Close(); err != nil {
			return fmt.Errorf("mark deleted: %w", err)
		}
		if err := client.UIDExpunge(uidSet).Close(); err != nil {
			return fmt.Errorf("expunge: %w", err)
		}
		return nil
	})
}

// AppendSent stores a copy of an already-sent RFC 5322 message in the Sent mailbox.
func (c *Client) AppendSent(ctx context.Context, creds Credentials, raw []byte) error {
	return withSession(ctx, creds, func(client *imapclient.Client) error {
		sent := findSpecialMailbox(client, imap.MailboxAttrSent, "Sent", "INBOX.Sent", "Sent Items", "Sent Messages", "Enviados", "Itens Enviados")
		if sent == "" {
			return ErrNoSentMailbox
		}
		cmd := client.Append(sent, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagSeen}, Time: time.Now()})
		if _, err := cmd.Write(raw); err != nil {
			_ = cmd.Close()
			return fmt.Errorf("append: %w", err)
		}
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("append: %w", err)
		}
		if _, err := cmd.Wait(); err != nil {
			return fmt.Errorf("append: %w", err)
		}
		return nil
	})
}

func withSession(ctx context.Context, creds Credentials, fn func(*imapclient.Client) error) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	client, stop, err := dialAndLogin(ctx, creds)
	if err != nil {
		return err
	}
	defer stop()
	defer client.Close()
	if err := fn(client); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("imap: %w", ctxErr)
		}
		return err
	}
	_ = client.Logout().Wait()
	return nil
}

// dialAndLogin returns a stop func that must be called once the session ends;
// until then, cancelling ctx closes the socket to unblock any pending command.
func dialAndLogin(ctx context.Context, creds Credentials) (*imapclient.Client, func() bool, error) {
	addr := net.JoinHostPort(creds.Host, strconv.Itoa(creds.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("imap dial: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	fail := func(format string, err error) (*imapclient.Client, func() bool, error) {
		stop()
		_ = conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			err = ctxErr
		}
		return nil, nil, fmt.Errorf(format, err)
	}
	opts := &imapclient.Options{
		TLSConfig:   &tls.Config{ServerName: creds.Host, MinVersion: tls.VersionTLS12},
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	}
	var client *imapclient.Client
	switch {
	case creds.UseTLS && creds.Port == 143:
		client, err = imapclient.NewStartTLS(conn, opts)
		if err != nil {
			return fail("imap starttls: %w", err)
		}
	case creds.UseTLS:
		tlsConn := tls.Client(conn, &tls.Config{ServerName: creds.Host, MinVersion: tls.VersionTLS12, NextProtos: []string{"imap"}})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fail("imap tls: %w", err)
		}
		client = imapclient.New(tlsConn, opts)
	default:
		client = imapclient.New(conn, opts)
	}
	if err := client.Login(creds.Username, creds.Password).Wait(); err != nil {
		_ = client.Close()
		return fail("imap login: %w", err)
	}
	return client, stop, nil
}

func moveOne(client *imapclient.Client, uid uint32, dest string) error {
	caps := client.Caps()
	if !caps.Has(imap.CapMove) && !caps.Has(imap.CapUIDPlus) {
		return ErrUnsafeExpunge
	}
	if _, err := client.Move(toUIDSet([]uint32{uid}), dest).Wait(); err != nil {
		return fmt.Errorf("move to %q: %w", dest, err)
	}
	return nil
}

func findSpecialMailbox(client *imapclient.Client, attr imap.MailboxAttr, fallbackNames ...string) string {
	var list []*imap.ListData
	var err error
	if client.Caps().Has(imap.CapSpecialUse) {
		list, err = client.List("", "*", &imap.ListOptions{ReturnSpecialUse: true}).Collect()
	}
	if list == nil || err != nil {
		if list, err = client.List("", "*", nil).Collect(); err != nil {
			return ""
		}
	}
	for _, m := range list {
		if slices.Contains(m.Attrs, attr) {
			return m.Mailbox
		}
	}
	for _, name := range fallbackNames {
		for _, m := range list {
			if strings.EqualFold(m.Mailbox, name) {
				return m.Mailbox
			}
		}
	}
	return ""
}

func buildCriteria(params SearchParams) *imap.SearchCriteria {
	criteria := &imap.SearchCriteria{}
	if params.Unseen {
		criteria.NotFlag = append(criteria.NotFlag, imap.FlagSeen)
	}
	if params.From != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "From", Value: params.From})
	}
	if params.To != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "To", Value: params.To})
	}
	if params.Subject != "" {
		criteria.Header = append(criteria.Header, imap.SearchCriteriaHeaderField{Key: "Subject", Value: params.Subject})
	}
	if params.Since != nil {
		criteria.Since = params.Since.UTC()
	}
	if params.Before != nil {
		criteria.Before = params.Before.UTC()
	}
	return criteria
}

func defaultFolder(folder string) string {
	folder = strings.TrimSpace(folder)
	if folder == "" {
		return "INBOX"
	}
	return folder
}

func delimiterString(delim rune) string {
	if delim == 0 {
		return ""
	}
	return string(delim)
}

func toUIDSet(uids []uint32) imap.UIDSet {
	out := make([]imap.UID, len(uids))
	for i, uid := range uids {
		out[i] = imap.UID(uid)
	}
	return imap.UIDSetNum(out...)
}

func summaryFromBuffer(folder string, msg *imapclient.FetchMessageBuffer) Summary {
	sum := Summary{
		UID:    uint32(msg.UID),
		Folder: folder,
		Size:   int(msg.RFC822Size),
		Date:   msg.InternalDate,
	}
	for _, f := range msg.Flags {
		sum.Flags = append(sum.Flags, string(f))
	}
	if msg.Envelope != nil {
		sum.Subject = msg.Envelope.Subject
		sum.From = mapAddresses(msg.Envelope.From)
		sum.To = mapAddresses(msg.Envelope.To)
		if !msg.Envelope.Date.IsZero() {
			sum.Date = msg.Envelope.Date
		}
	}
	return sum
}

func mapAddresses(addrs []imap.Address) []Address {
	out := make([]Address, 0, len(addrs))
	for _, a := range addrs {
		addr := a.Addr()
		if addr == "" {
			continue
		}
		out = append(out, Address{Name: a.Name, Address: addr})
	}
	return out
}

func toIMAPFlags(names []string) []imap.Flag {
	out := make([]imap.Flag, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !strings.HasPrefix(n, "\\") {
			switch strings.ToLower(n) {
			case "seen", "read":
				n = string(imap.FlagSeen)
			case "deleted":
				n = string(imap.FlagDeleted)
			case "flagged":
				n = string(imap.FlagFlagged)
			case "answered":
				n = string(imap.FlagAnswered)
			case "draft":
				n = string(imap.FlagDraft)
			}
		}
		out = append(out, imap.Flag(n))
	}
	return out
}

func parseMIME(raw []byte) (text, html string, truncated bool, attachments []AttachmentMeta) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		text, truncated = truncateRunes(string(raw), maxBodyBytes)
		return text, "", truncated, nil
	}
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			ct = strings.ToLower(ct)
			isHTML := ct == "text/html"
			if !isHTML && ct != "text/plain" && ct != "" {
				n, _ := io.Copy(io.Discard, p.Body)
				attachments = append(attachments, AttachmentMeta{MIMEType: ct, Size: int(n)})
				continue
			}
			b, _ := io.ReadAll(io.LimitReader(p.Body, int64(maxBodyBytes)+1))
			body, trunc := truncateRunes(string(b), maxBodyBytes)
			if trunc {
				truncated = true
			}
			if isHTML {
				if html == "" {
					html = body
				}
			} else if text == "" {
				text = body
			}
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			ct, _, _ := h.ContentType()
			n, _ := io.Copy(io.Discard, p.Body)
			attachments = append(attachments, AttachmentMeta{Filename: filename, MIMEType: ct, Size: int(n)})
		}
	}
	return text, html, truncated, attachments
}

func truncateRunes(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	if maxBytes <= 0 {
		return "", true
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}
