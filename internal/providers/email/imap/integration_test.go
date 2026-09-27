package imapmail

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

const testMessage = "From: a@example.com\r\nTo: me@example.com\r\nSubject: Hello\r\nMessage-ID: <m1@example.com>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nBody text\r\n"

type memServer struct {
	creds Credentials
	user  *imapmemserver.User
}

func startMemServer(t *testing.T, caps imap.CapSet, specialUse bool) memServer {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me", "pw")
	mem.AddUser(user)
	for _, name := range []string{"INBOX", "Archive"} {
		if err := user.Create(name, nil); err != nil {
			t.Fatal(err)
		}
	}
	for name, attr := range map[string]imap.MailboxAttr{"Trash": imap.MailboxAttrTrash, "Sent": imap.MailboxAttrSent} {
		var options *imap.CreateOptions
		if specialUse {
			options = &imap.CreateOptions{SpecialUse: []imap.MailboxAttr{attr}}
		}
		if err := user.Create(name, options); err != nil {
			t.Fatal(err)
		}
	}

	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         caps,
		InsecureAuth: true,
		Logger:       discardLogger{},
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	addr := listener.Addr().(*net.TCPAddr)
	return memServer{
		creds: Credentials{Host: "127.0.0.1", Port: addr.Port, Username: "me", Password: "pw"},
		user:  user,
	}
}

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

func (s memServer) appendMessage(t *testing.T, mailbox string, flags ...imap.Flag) uint32 {
	t.Helper()
	data, err := s.user.Append(mailbox, literal(testMessage), &imap.AppendOptions{Flags: flags, Time: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return uint32(data.UID)
}

func literal(s string) imap.LiteralReader {
	return &literalReader{Reader: strings.NewReader(s), size: int64(len(s))}
}

type literalReader struct {
	*strings.Reader
	size int64
}

func (l *literalReader) Size() int64 { return l.size }

func fullCaps() imap.CapSet {
	return imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapMove: {}, imap.CapSpecialUse: {}}
}

func flagsOf(t *testing.T, c *Client, creds Credentials, folder string, uid uint32) []string {
	t.Helper()
	list, err := c.Search(context.Background(), creds, SearchParams{Folder: folder})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if s.UID == uid {
			return s.Flags
		}
	}
	t.Fatalf("uid %d not found in %s", uid, folder)
	return nil
}

func TestGetDoesNotMarkSeenAndMarkReadToggles(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, fullCaps(), true)
	uid := srv.appendMessage(t, "INBOX")
	c := NewClient()
	ctx := context.Background()

	msg, err := c.Get(ctx, srv.creds, "INBOX", uid)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Subject != "Hello" || strings.TrimSpace(msg.BodyText) != "Body text" {
		t.Fatalf("message = %+v", msg)
	}
	if slices.Contains(flagsOf(t, c, srv.creds, "INBOX", uid), string(imap.FlagSeen)) {
		t.Fatal("Get marked the message as seen")
	}

	if err := c.SetFlags(ctx, srv.creds, "INBOX", []uint32{uid}, FlagUpdate{Add: []string{"seen"}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(flagsOf(t, c, srv.creds, "INBOX", uid), string(imap.FlagSeen)) {
		t.Fatal("message not marked as seen")
	}
	if err := c.SetFlags(ctx, srv.creds, "INBOX", []uint32{uid}, FlagUpdate{Remove: []string{"seen"}}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(flagsOf(t, c, srv.creds, "INBOX", uid), string(imap.FlagSeen)) {
		t.Fatal("message still seen after unmark")
	}

	if _, err := c.Get(ctx, srv.creds, "INBOX", uid+100); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("missing uid err = %v", err)
	}
}

func TestSearchReturnsNewestFirst(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, fullCaps(), true)
	first := srv.appendMessage(t, "INBOX")
	second := srv.appendMessage(t, "INBOX")
	third := srv.appendMessage(t, "INBOX")
	list, err := NewClient().Search(context.Background(), srv.creds, SearchParams{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].UID != third || list[1].UID != second || first == 0 {
		t.Fatalf("uids = %+v", list)
	}
}

func TestDeleteMovesToTrash(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, fullCaps(), true)
	uid := srv.appendMessage(t, "INBOX")
	c := NewClient()
	if err := c.Delete(context.Background(), srv.creds, "INBOX", uid); err != nil {
		t.Fatal(err)
	}
	inbox, _ := c.Search(context.Background(), srv.creds, SearchParams{})
	trash, _ := c.Search(context.Background(), srv.creds, SearchParams{Folder: "Trash"})
	if len(inbox) != 0 || len(trash) != 1 {
		t.Fatalf("inbox = %d, trash = %d", len(inbox), len(trash))
	}
}

func TestDeleteInTrashOnlyExpungesTargetMessage(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, fullCaps(), true)
	target := srv.appendMessage(t, "Trash")
	bystander := srv.appendMessage(t, "Trash", imap.FlagDeleted)
	c := NewClient()
	if err := c.Delete(context.Background(), srv.creds, "Trash", target); err != nil {
		t.Fatal(err)
	}
	trash, err := c.Search(context.Background(), srv.creds, SearchParams{Folder: "Trash"})
	if err != nil {
		t.Fatal(err)
	}
	if len(trash) != 1 || trash[0].UID != bystander {
		t.Fatalf("trash = %+v, want only bystander %d", trash, bystander)
	}
}

func TestDeleteRefusesUnsafeExpunge(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, imap.CapSet{imap.CapIMAP4rev1: {}}, false)
	uid := srv.appendMessage(t, "Trash")
	if err := NewClient().Delete(context.Background(), srv.creds, "Trash", uid); !errors.Is(err, ErrUnsafeExpunge) {
		t.Fatalf("err = %v, want ErrUnsafeExpunge", err)
	}
}

func TestMoveAndAppendSent(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, fullCaps(), true)
	uid := srv.appendMessage(t, "INBOX")
	c := NewClient()
	ctx := context.Background()
	if err := c.Move(ctx, srv.creds, "INBOX", uid, "Archive"); err != nil {
		t.Fatal(err)
	}
	archive, _ := c.Search(ctx, srv.creds, SearchParams{Folder: "Archive"})
	if len(archive) != 1 {
		t.Fatalf("archive = %d", len(archive))
	}

	if err := c.AppendSent(ctx, srv.creds, []byte(testMessage)); err != nil {
		t.Fatal(err)
	}
	sent, _ := c.Search(ctx, srv.creds, SearchParams{Folder: "Sent"})
	if len(sent) != 1 || !slices.Contains(sent[0].Flags, string(imap.FlagSeen)) {
		t.Fatalf("sent = %+v", sent)
	}
}

func TestAppendSentFallsBackToMailboxName(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}, false)
	if err := NewClient().AppendSent(context.Background(), srv.creds, []byte(testMessage)); err != nil {
		t.Fatal(err)
	}
}

func TestLoginFailureAndTimeout(t *testing.T) {
	t.Parallel()
	srv := startMemServer(t, fullCaps(), true)
	bad := srv.creds
	bad.Password = "wrong"
	if err := NewClient().Test(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "imap login") {
		t.Fatalf("err = %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			time.Sleep(2 * time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	silent := Credentials{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "me", Password: "pw"}
	started := time.Now()
	if err := NewClient().Test(ctx, silent); err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("hung for %v despite context deadline", time.Since(started))
	}
}
