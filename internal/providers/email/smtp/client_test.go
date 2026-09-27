package smtpmail

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildMessageSetsThreadingHeadersAndHidesBCC(t *testing.T) {
	t.Parallel()
	msg, err := buildMessage("me@example.com", SendParams{
		To: []string{"a@example.com"}, BCC: []string{"secret@example.com"}, Subject: "Re: Oi",
		BodyText: "texto", BodyHTML: "<p>html</p>", InReplyTo: "<orig@example.com>", References: "<orig@example.com>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.GetMessageID() == "" {
		t.Fatal("Message-ID was not generated")
	}
	var raw bytes.Buffer
	if _, err := msg.WriteTo(&raw); err != nil {
		t.Fatal(err)
	}
	out := raw.String()
	for _, want := range []string{"In-Reply-To: <orig@example.com>", "References: <orig@example.com>", "multipart/alternative"} {
		if !strings.Contains(out, want) {
			t.Fatalf("raw message missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret@example.com") {
		t.Fatal("BCC recipient leaked into message headers")
	}
}

func TestBuildMessageRequiresRecipient(t *testing.T) {
	t.Parallel()
	if _, err := buildMessage("me@example.com", SendParams{}); err == nil {
		t.Fatal("expected error without recipients")
	}
}
