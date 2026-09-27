package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	emailcrypto "github.com/leofelipet/contexta/internal/email/crypto"
	imapmail "github.com/leofelipet/contexta/internal/providers/email/imap"
	smtpmail "github.com/leofelipet/contexta/internal/providers/email/smtp"
)

const (
	MaxBatchUIDs = 100
	// Send + Sent-folder append must finish inside the HTTP server WriteTimeout (30s).
	sendBudget = 28 * time.Second
)

var (
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrNotFound         = errors.New("not found")
	ErrMessageNotFound  = errors.New("email message not found")
	ErrDisabled         = errors.New("email account is disabled")
	ErrMissingKey       = emailcrypto.ErrMissingKey
	ErrProvider         = errors.New("email provider error")
	autoSaveSentServers = []string{"imap.gmail.com", "outlook.office365.com", "imap-mail.outlook.com"}
)

// ProviderError wraps IMAP/SMTP failures so callers can surface the server
// message (never credentials) instead of a generic error.
type ProviderError struct {
	Protocol string
	Err      error
}

func (e *ProviderError) Error() string { return e.Protocol + ": " + e.Err.Error() }
func (e *ProviderError) Unwrap() error { return e.Err }
func (e *ProviderError) Is(target error) bool {
	return target == ErrProvider
}

type AccountStore interface {
	ListEmailAccounts(context.Context, ListParams) (Page, error)
	GetEmailAccount(context.Context, string) (Account, error)
	GetEmailAccountSecrets(context.Context, string) (AccountSecrets, []byte, error)
	CreateEmailAccount(context.Context, CreateParams, []byte) (Account, error)
	UpdateEmailAccount(context.Context, string, UpdateParams, []byte) (Account, error)
	DeleteEmailAccount(context.Context, string) error
}

type IMAPClient interface {
	Test(context.Context, imapmail.Credentials) error
	ListMailboxes(context.Context, imapmail.Credentials) ([]imapmail.Mailbox, error)
	Search(context.Context, imapmail.Credentials, imapmail.SearchParams) ([]imapmail.Summary, error)
	Get(context.Context, imapmail.Credentials, string, uint32) (imapmail.Message, error)
	SetFlags(context.Context, imapmail.Credentials, string, []uint32, imapmail.FlagUpdate) error
	Move(context.Context, imapmail.Credentials, string, uint32, string) error
	Delete(context.Context, imapmail.Credentials, string, uint32) error
	AppendSent(context.Context, imapmail.Credentials, []byte) error
}

type SMTPClient interface {
	Test(context.Context, smtpmail.Credentials) error
	Send(context.Context, smtpmail.Credentials, smtpmail.SendParams) (smtpmail.SentMessage, error)
}

type Service struct {
	store AccountStore
	key   []byte
	imap  IMAPClient
	smtp  SMTPClient
}

func NewService(store AccountStore, credentialsKey []byte, imapClient IMAPClient, smtpClient SMTPClient) *Service {
	if imapClient == nil {
		imapClient = imapmail.NewClient()
	}
	if smtpClient == nil {
		smtpClient = smtpmail.NewClient()
	}
	return &Service{store: store, key: credentialsKey, imap: imapClient, smtp: smtpClient}
}

// DefaultSaveSentCopy is false for providers that already file SMTP submissions in Sent.
func DefaultSaveSentCopy(imapHost string) bool {
	host := strings.ToLower(strings.TrimSpace(imapHost))
	for _, known := range autoSaveSentServers {
		if host == known {
			return false
		}
	}
	return true
}

func (s *Service) ListAccounts(ctx context.Context, params ListParams) (Page, error) {
	return s.store.ListEmailAccounts(ctx, params)
}

func (s *Service) GetAccount(ctx context.Context, id string) (Account, error) {
	return s.store.GetEmailAccount(ctx, id)
}

func (s *Service) CreateAccount(ctx context.Context, params CreateParams) (Account, error) {
	if err := s.requireKey(); err != nil {
		return Account{}, err
	}
	normalized, err := normalizeCreate(params)
	if err != nil {
		return Account{}, err
	}
	ciphertext, err := emailcrypto.Encrypt(s.key, []byte(normalized.Password))
	if err != nil {
		return Account{}, err
	}
	return s.store.CreateEmailAccount(ctx, normalized, ciphertext)
}

func (s *Service) UpdateAccount(ctx context.Context, id string, params UpdateParams) (Account, error) {
	var ciphertext []byte
	if params.Password != nil {
		if err := s.requireKey(); err != nil {
			return Account{}, err
		}
		if strings.TrimSpace(*params.Password) == "" {
			return Account{}, ErrInvalidArgument
		}
		var err error
		ciphertext, err = emailcrypto.Encrypt(s.key, []byte(*params.Password))
		if err != nil {
			return Account{}, err
		}
	}
	return s.store.UpdateEmailAccount(ctx, id, params, ciphertext)
}

func (s *Service) DeleteAccount(ctx context.Context, id string) error {
	return s.store.DeleteEmailAccount(ctx, id)
}

// TestAccount checks IMAP and SMTP in parallel so both fit in one request timeout.
func (s *Service) TestAccount(ctx context.Context, id string) (TestResult, error) {
	secrets, err := s.loadSecrets(ctx, id, false)
	if err != nil {
		return TestResult{}, err
	}
	var result TestResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := s.imap.Test(ctx, imapCreds(secrets)); err != nil {
			result.IMAPError = err.Error()
			return
		}
		result.IMAPOK = true
	}()
	go func() {
		defer wg.Done()
		if err := s.smtp.Test(ctx, smtpCreds(secrets)); err != nil {
			result.SMTPError = err.Error()
			return
		}
		result.SMTPOK = true
	}()
	wg.Wait()
	return result, nil
}

func (s *Service) ListMailboxes(ctx context.Context, accountID string) ([]Mailbox, error) {
	secrets, err := s.loadSecrets(ctx, accountID, true)
	if err != nil {
		return nil, err
	}
	list, err := s.imap.ListMailboxes(ctx, imapCreds(secrets))
	if err != nil {
		return nil, providerError("imap", err)
	}
	out := make([]Mailbox, len(list))
	for i, m := range list {
		out[i] = Mailbox{Name: m.Name, Delimiter: m.Delimiter, Attributes: m.Attributes}
	}
	return out, nil
}

func (s *Service) SearchEmails(ctx context.Context, accountID string, params SearchParams) ([]EmailSummary, error) {
	secrets, err := s.loadSecrets(ctx, accountID, true)
	if err != nil {
		return nil, err
	}
	list, err := s.imap.Search(ctx, imapCreds(secrets), imapmail.SearchParams{
		Folder: params.Folder, Unseen: params.Unseen, From: params.From, To: params.To,
		Subject: params.Subject, Since: params.Since, Before: params.Before, Limit: params.Limit,
	})
	if err != nil {
		return nil, providerError("imap", err)
	}
	out := make([]EmailSummary, len(list))
	for i, m := range list {
		out[i] = mapSummary(m)
	}
	return out, nil
}

func (s *Service) GetEmail(ctx context.Context, accountID, folder string, uid uint32) (EmailMessage, error) {
	if uid == 0 {
		return EmailMessage{}, ErrInvalidArgument
	}
	secrets, err := s.loadSecrets(ctx, accountID, true)
	if err != nil {
		return EmailMessage{}, err
	}
	msg, err := s.imap.Get(ctx, imapCreds(secrets), folder, uid)
	if err != nil {
		return EmailMessage{}, providerError("imap", err)
	}
	return mapMessage(msg), nil
}

func (s *Service) SendEmail(ctx context.Context, accountID string, params SendParams) (SendResult, error) {
	if len(params.To) == 0 {
		return SendResult{}, ErrInvalidArgument
	}
	secrets, err := s.loadSecrets(ctx, accountID, true)
	if err != nil {
		return SendResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, sendBudget)
	defer cancel()
	sent, err := s.smtp.Send(ctx, smtpCreds(secrets), smtpmail.SendParams{
		To: params.To, CC: params.CC, BCC: params.BCC, Subject: params.Subject,
		BodyText: params.BodyText, BodyHTML: params.BodyHTML,
		InReplyTo: params.InReplyTo, References: params.References,
	})
	if err != nil {
		return SendResult{}, providerError("smtp", err)
	}
	result := SendResult{MessageID: sent.MessageID}
	if !secrets.SaveSentCopy {
		return result, nil
	}
	if len(sent.Raw) == 0 {
		result.SaveSentError = "message could not be serialized for the Sent folder"
		return result, nil
	}
	if err := s.imap.AppendSent(ctx, imapCreds(secrets), sent.Raw); err != nil {
		result.SaveSentError = err.Error()
		return result, nil
	}
	result.SavedToSent = true
	return result, nil
}

// SetEmailFlags applies the same flag update to one or more messages in a folder.
func (s *Service) SetEmailFlags(ctx context.Context, accountID, folder string, uids []uint32, update FlagUpdate) error {
	if err := validateUIDs(uids); err != nil {
		return err
	}
	if len(update.Add) == 0 && len(update.Remove) == 0 {
		return ErrInvalidArgument
	}
	secrets, err := s.loadSecrets(ctx, accountID, true)
	if err != nil {
		return err
	}
	if err := s.imap.SetFlags(ctx, imapCreds(secrets), folder, uids, imapmail.FlagUpdate{Add: update.Add, Remove: update.Remove}); err != nil {
		return providerError("imap", err)
	}
	return nil
}

func (s *Service) MarkRead(ctx context.Context, accountID, folder string, uids []uint32, read bool) error {
	update := FlagUpdate{Add: []string{"seen"}}
	if !read {
		update = FlagUpdate{Remove: []string{"seen"}}
	}
	return s.SetEmailFlags(ctx, accountID, folder, uids, update)
}

func (s *Service) MoveEmail(ctx context.Context, accountID, folder string, uid uint32, dest string) error {
	if uid == 0 || strings.TrimSpace(dest) == "" {
		return ErrInvalidArgument
	}
	secrets, err := s.loadSecrets(ctx, accountID, true)
	if err != nil {
		return err
	}
	if err := s.imap.Move(ctx, imapCreds(secrets), folder, uid, dest); err != nil {
		return providerError("imap", err)
	}
	return nil
}

func (s *Service) DeleteEmail(ctx context.Context, accountID, folder string, uid uint32) error {
	if uid == 0 {
		return ErrInvalidArgument
	}
	secrets, err := s.loadSecrets(ctx, accountID, true)
	if err != nil {
		return err
	}
	if err := s.imap.Delete(ctx, imapCreds(secrets), folder, uid); err != nil {
		return providerError("imap", err)
	}
	return nil
}

func (s *Service) requireKey() error {
	if len(s.key) != 32 {
		return ErrMissingKey
	}
	return nil
}

func (s *Service) loadSecrets(ctx context.Context, id string, requireEnabled bool) (AccountSecrets, error) {
	if err := s.requireKey(); err != nil {
		return AccountSecrets{}, err
	}
	acc, ciphertext, err := s.store.GetEmailAccountSecrets(ctx, id)
	if err != nil {
		return AccountSecrets{}, err
	}
	if requireEnabled && !acc.Enabled {
		return AccountSecrets{}, ErrDisabled
	}
	password, err := emailcrypto.Decrypt(s.key, ciphertext)
	if err != nil {
		return AccountSecrets{}, fmt.Errorf("decrypt credentials: %w", err)
	}
	acc.Password = string(password)
	return acc, nil
}

func providerError(protocol string, err error) error {
	if errors.Is(err, imapmail.ErrMessageNotFound) {
		return ErrMessageNotFound
	}
	return &ProviderError{Protocol: protocol, Err: err}
}

func validateUIDs(uids []uint32) error {
	if len(uids) == 0 || len(uids) > MaxBatchUIDs {
		return ErrInvalidArgument
	}
	for _, uid := range uids {
		if uid == 0 {
			return ErrInvalidArgument
		}
	}
	return nil
}

func imapCreds(s AccountSecrets) imapmail.Credentials {
	return imapmail.Credentials{
		Host: s.IMAPHost, Port: s.IMAPPort, UseTLS: s.IMAPTLS,
		Username: s.Username, Password: s.Password,
	}
}

func smtpCreds(s AccountSecrets) smtpmail.Credentials {
	return smtpmail.Credentials{
		Host: s.SMTPHost, Port: s.SMTPPort, UseTLS: s.SMTPTLS,
		Username: s.Username, Password: s.Password, From: s.Address,
	}
}

func mapSummary(m imapmail.Summary) EmailSummary {
	return EmailSummary{
		UID: m.UID, Folder: m.Folder, Subject: m.Subject,
		From: mapAddrs(m.From), To: mapAddrs(m.To),
		Date: m.Date, Flags: m.Flags, Size: m.Size,
	}
}

func mapMessage(m imapmail.Message) EmailMessage {
	attachments := make([]AttachmentMeta, len(m.Attachments))
	for i, a := range m.Attachments {
		attachments[i] = AttachmentMeta{Filename: a.Filename, MIMEType: a.MIMEType, Size: a.Size}
	}
	return EmailMessage{
		EmailSummary:  mapSummary(m.Summary),
		CC:            mapAddrs(m.CC),
		BCC:           mapAddrs(m.BCC),
		ReplyTo:       mapAddrs(m.ReplyTo),
		MessageID:     m.MessageID,
		InReplyTo:     m.InReplyTo,
		BodyText:      m.BodyText,
		BodyHTML:      m.BodyHTML,
		BodyTruncated: m.BodyTruncated,
		Attachments:   attachments,
	}
}

func mapAddrs(in []imapmail.Address) []Address {
	out := make([]Address, len(in))
	for i, a := range in {
		out[i] = Address{Name: a.Name, Address: a.Address}
	}
	return out
}

func normalizeCreate(params CreateParams) (CreateParams, error) {
	params.Name = strings.TrimSpace(params.Name)
	params.Address = strings.TrimSpace(params.Address)
	params.Username = strings.TrimSpace(params.Username)
	params.IMAPHost = strings.TrimSpace(params.IMAPHost)
	params.SMTPHost = strings.TrimSpace(params.SMTPHost)
	if params.Name == "" || params.Address == "" || params.Username == "" || strings.TrimSpace(params.Password) == "" ||
		params.IMAPHost == "" || params.SMTPHost == "" {
		return CreateParams{}, ErrInvalidArgument
	}
	if _, err := mail.ParseAddress(params.Address); err != nil {
		return CreateParams{}, ErrInvalidArgument
	}
	if params.IMAPPort < 1 || params.IMAPPort > 65535 || params.SMTPPort < 1 || params.SMTPPort > 65535 {
		return CreateParams{}, ErrInvalidArgument
	}
	if params.SaveSentCopy == nil {
		saveSent := DefaultSaveSentCopy(params.IMAPHost)
		params.SaveSentCopy = &saveSent
	}
	return params, nil
}
