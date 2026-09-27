package email

import (
	"context"
	"errors"
	"slices"
	"testing"

	emailcrypto "github.com/leofelipet/contexta/internal/email/crypto"
	imapmail "github.com/leofelipet/contexta/internal/providers/email/imap"
	smtpmail "github.com/leofelipet/contexta/internal/providers/email/smtp"
)

const testAccountID = "00000000-0000-0000-0000-0000000000aa"

type fakeStore struct {
	account    Account
	ciphertext []byte
	created    CreateParams
	createdKey []byte
}

func (f *fakeStore) ListEmailAccounts(context.Context, ListParams) (Page, error) { return Page{}, nil }
func (f *fakeStore) GetEmailAccount(context.Context, string) (Account, error) {
	return f.account, nil
}
func (f *fakeStore) GetEmailAccountSecrets(_ context.Context, id string) (AccountSecrets, []byte, error) {
	if id != f.account.ID {
		return AccountSecrets{}, nil, ErrNotFound
	}
	return AccountSecrets{Account: f.account}, f.ciphertext, nil
}
func (f *fakeStore) CreateEmailAccount(_ context.Context, params CreateParams, ciphertext []byte) (Account, error) {
	f.created, f.createdKey = params, ciphertext
	return Account{ID: testAccountID}, nil
}
func (f *fakeStore) UpdateEmailAccount(context.Context, string, UpdateParams, []byte) (Account, error) {
	return f.account, nil
}
func (f *fakeStore) DeleteEmailAccount(context.Context, string) error { return nil }

type fakeIMAP struct {
	testErr     error
	getErr      error
	appendErr   error
	flagUIDs    []uint32
	flagUpdate  imapmail.FlagUpdate
	appendedRaw []byte
	password    string
}

func (f *fakeIMAP) Test(_ context.Context, c imapmail.Credentials) error {
	f.password = c.Password
	return f.testErr
}
func (f *fakeIMAP) ListMailboxes(context.Context, imapmail.Credentials) ([]imapmail.Mailbox, error) {
	return nil, nil
}
func (f *fakeIMAP) Search(context.Context, imapmail.Credentials, imapmail.SearchParams) ([]imapmail.Summary, error) {
	return nil, nil
}
func (f *fakeIMAP) Get(context.Context, imapmail.Credentials, string, uint32) (imapmail.Message, error) {
	return imapmail.Message{}, f.getErr
}
func (f *fakeIMAP) SetFlags(_ context.Context, _ imapmail.Credentials, _ string, uids []uint32, update imapmail.FlagUpdate) error {
	f.flagUIDs, f.flagUpdate = uids, update
	return nil
}
func (f *fakeIMAP) Move(context.Context, imapmail.Credentials, string, uint32, string) error {
	return nil
}
func (f *fakeIMAP) Delete(context.Context, imapmail.Credentials, string, uint32) error { return nil }
func (f *fakeIMAP) AppendSent(_ context.Context, _ imapmail.Credentials, raw []byte) error {
	f.appendedRaw = raw
	return f.appendErr
}

type fakeSMTP struct {
	testErr error
	sendErr error
}

func (f *fakeSMTP) Test(context.Context, smtpmail.Credentials) error { return f.testErr }
func (f *fakeSMTP) Send(context.Context, smtpmail.Credentials, smtpmail.SendParams) (smtpmail.SentMessage, error) {
	if f.sendErr != nil {
		return smtpmail.SentMessage{}, f.sendErr
	}
	return smtpmail.SentMessage{MessageID: "abc@example.com", Raw: []byte("raw message")}, nil
}

func newTestService(t *testing.T, account Account) (*Service, *fakeStore, *fakeIMAP, *fakeSMTP) {
	t.Helper()
	key := make([]byte, 32)
	ciphertext, err := emailcrypto.Encrypt(key, []byte(" secret with spaces "))
	if err != nil {
		t.Fatal(err)
	}
	account.ID = testAccountID
	store := &fakeStore{account: account, ciphertext: ciphertext}
	imapClient, smtpClient := &fakeIMAP{}, &fakeSMTP{}
	return NewService(store, key, imapClient, smtpClient), store, imapClient, smtpClient
}

func TestMarkReadAddsOrRemovesSeen(t *testing.T) {
	t.Parallel()
	service, _, imapClient, _ := newTestService(t, Account{Enabled: true})

	if err := service.MarkRead(context.Background(), testAccountID, "INBOX", []uint32{3, 7}, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(imapClient.flagUIDs, []uint32{3, 7}) || !slices.Equal(imapClient.flagUpdate.Add, []string{"seen"}) || len(imapClient.flagUpdate.Remove) != 0 {
		t.Fatalf("mark read = %v %+v", imapClient.flagUIDs, imapClient.flagUpdate)
	}
	if err := service.MarkRead(context.Background(), testAccountID, "INBOX", []uint32{3}, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(imapClient.flagUpdate.Remove, []string{"seen"}) || len(imapClient.flagUpdate.Add) != 0 {
		t.Fatalf("mark unread = %+v", imapClient.flagUpdate)
	}
}

func TestMarkReadValidatesUIDs(t *testing.T) {
	t.Parallel()
	service, _, _, _ := newTestService(t, Account{Enabled: true})
	tooMany := make([]uint32, MaxBatchUIDs+1)
	for i := range tooMany {
		tooMany[i] = uint32(i + 1)
	}
	for name, uids := range map[string][]uint32{"empty": nil, "zero": {1, 0}, "too many": tooMany} {
		if err := service.MarkRead(context.Background(), testAccountID, "", uids, true); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%s: err = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestDisabledAccountRejectsMailboxOperations(t *testing.T) {
	t.Parallel()
	service, _, _, _ := newTestService(t, Account{Enabled: false})
	if err := service.MarkRead(context.Background(), testAccountID, "", []uint32{1}, true); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v, want ErrDisabled", err)
	}
}

func TestSendEmailSavesSentCopyWhenEnabled(t *testing.T) {
	t.Parallel()
	service, _, imapClient, _ := newTestService(t, Account{Enabled: true, SaveSentCopy: true})
	result, err := service.SendEmail(context.Background(), testAccountID, SendParams{To: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.SavedToSent || result.MessageID != "abc@example.com" || string(imapClient.appendedRaw) != "raw message" {
		t.Fatalf("result = %+v, appended = %q", result, imapClient.appendedRaw)
	}
}

func TestSendEmailSkipsSentCopyWhenDisabled(t *testing.T) {
	t.Parallel()
	service, _, imapClient, _ := newTestService(t, Account{Enabled: true, SaveSentCopy: false})
	result, err := service.SendEmail(context.Background(), testAccountID, SendParams{To: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SavedToSent || imapClient.appendedRaw != nil {
		t.Fatalf("result = %+v, appended = %q", result, imapClient.appendedRaw)
	}
}

func TestSendEmailReportsSentCopyFailureWithoutFailingSend(t *testing.T) {
	t.Parallel()
	service, _, imapClient, _ := newTestService(t, Account{Enabled: true, SaveSentCopy: true})
	imapClient.appendErr = imapmail.ErrNoSentMailbox
	result, err := service.SendEmail(context.Background(), testAccountID, SendParams{To: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SavedToSent || result.SaveSentError == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestProviderErrorsAreWrapped(t *testing.T) {
	t.Parallel()
	service, _, imapClient, smtpClient := newTestService(t, Account{Enabled: true})

	smtpClient.sendErr = errors.New("535 authentication failed")
	_, err := service.SendEmail(context.Background(), testAccountID, SendParams{To: []string{"a@example.com"}})
	if !errors.Is(err, ErrProvider) || err.Error() != "smtp: 535 authentication failed" {
		t.Fatalf("send err = %v", err)
	}

	imapClient.getErr = imapmail.ErrMessageNotFound
	if _, err := service.GetEmail(context.Background(), testAccountID, "INBOX", 9); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("get err = %v, want ErrMessageNotFound", err)
	}
}

func TestTestAccountReportsEachProtocol(t *testing.T) {
	t.Parallel()
	service, _, imapClient, smtpClient := newTestService(t, Account{Enabled: false})
	smtpClient.testErr = errors.New("smtp: dial timeout")
	result, err := service.TestAccount(context.Background(), testAccountID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IMAPOK || result.SMTPOK || result.SMTPError == "" || result.IMAPError != "" {
		t.Fatalf("result = %+v", result)
	}
	if imapClient.password != " secret with spaces " {
		t.Fatalf("password was altered: %q", imapClient.password)
	}
}

func TestCreateAccountKeepsPasswordAndDefaultsSaveSent(t *testing.T) {
	t.Parallel()
	service, store, _, _ := newTestService(t, Account{})
	_, err := service.CreateAccount(context.Background(), CreateParams{
		Name: "Work", Address: "me@example.com", Username: "me", Password: " p@ss ",
		IMAPHost: "imap.gmail.com", IMAPPort: 993, SMTPHost: "smtp.gmail.com", SMTPPort: 465,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.created.Password != " p@ss " {
		t.Fatalf("password = %q", store.created.Password)
	}
	if store.created.SaveSentCopy == nil || *store.created.SaveSentCopy {
		t.Fatal("gmail accounts should default save_sent_copy to false")
	}
	plain, err := emailcrypto.Decrypt(make([]byte, 32), store.createdKey)
	if err != nil || string(plain) != " p@ss " {
		t.Fatalf("ciphertext decrypts to %q, err = %v", plain, err)
	}
}

func TestDefaultSaveSentCopy(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"imap.gmail.com":        false,
		"Outlook.Office365.com": false,
		"mail.empresa.com.br":   true,
	}
	for host, want := range cases {
		if got := DefaultSaveSentCopy(host); got != want {
			t.Fatalf("DefaultSaveSentCopy(%q) = %v, want %v", host, got, want)
		}
	}
}
