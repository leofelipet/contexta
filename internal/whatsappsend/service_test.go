package whatsappsend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/leofelipet/contexta/internal/providers/whatsapp/uazapi"
)

func TestNormalizeRecipient(t *testing.T) {
	t.Parallel()
	valid := map[string]string{
		"5511999999999":                "5511999999999@s.whatsapp.net",
		"+55 (11) 99999-9999":          "5511999999999@s.whatsapp.net",
		"+55.11.99999.9999":            "5511999999999@s.whatsapp.net",
		"5511999999999@s.whatsapp.net": "5511999999999@s.whatsapp.net",
		"12345678":                     "12345678@s.whatsapp.net",
	}
	for input, want := range valid {
		if got, err := NormalizeRecipient(input); err != nil || got != want {
			t.Errorf("NormalizeRecipient(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"120363000000000000@g.us", "123456789@lid", "120363000000000000@newsletter", "status@broadcast",
		"5511999999999@example.com", "+55@s.whatsapp.net", "abc5511999999", "55119999x9999", "1234567", "1234567890123456", "", "@s.whatsapp.net",
	} {
		if _, err := NormalizeRecipient(input); !errors.Is(err, ErrInvalidRecipient) {
			t.Errorf("NormalizeRecipient(%q) error = %v, want ErrInvalidRecipient", input, err)
		}
	}
}

type fakeSender struct {
	calls        int
	number, text string
	err          error
}

func (f *fakeSender) SendText(_ context.Context, number, text string) (uazapi.SendTextResult, error) {
	f.calls++
	f.number, f.text = number, text
	return uazapi.SendTextResult{MessageID: "msg-1", Status: "Pending"}, f.err
}

func TestSendRejectsWithoutCallingSender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, recipient, text string
		want                  error
	}{
		{"group", "120363000000000000@g.us", "hi", ErrInvalidRecipient},
		{"lid", "123456789@lid", "hi", ErrInvalidRecipient},
		{"empty text", "5511999999999", "", ErrInvalidText},
		{"whitespace text", "5511999999999", " \n\t", ErrInvalidText},
		{"placeholder", "5511999999999", "Oi {{name}}", ErrInvalidText},
		{"too long", "5511999999999", strings.Repeat("é", MaxTextRunes+1), ErrInvalidText},
	}
	for _, test := range tests {
		sender := &fakeSender{}
		if _, err := NewService(sender).Send(context.Background(), test.recipient, test.text); !errors.Is(err, test.want) || sender.calls != 0 {
			t.Errorf("%s: error = %v, calls = %d", test.name, err, sender.calls)
		}
	}
}

func TestSendDeliversExactText(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{}
	text := "  Olá, tudo bem? {single}\n" + strings.Repeat("a", MaxTextRunes-27)
	result, err := NewService(sender).Send(context.Background(), "+55 11 99999-9999", text)
	if err != nil || sender.calls != 1 || sender.number != "5511999999999@s.whatsapp.net" || sender.text != text {
		t.Fatalf("result = %#v, error = %v, sender = %#v", result, err, sender)
	}
	if result != (Result{RecipientJID: "5511999999999@s.whatsapp.net", MessageID: "msg-1", Status: "Pending"}) {
		t.Fatalf("result = %#v", result)
	}
}

func TestSendPropagatesSenderError(t *testing.T) {
	t.Parallel()
	remote := errors.New("UAZAPI returned status 500")
	sender := &fakeSender{err: remote}
	result, err := NewService(sender).Send(context.Background(), "5511999999999", "hi")
	if !errors.Is(err, remote) || result.RecipientJID != "5511999999999@s.whatsapp.net" || result.MessageID != "" {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}
