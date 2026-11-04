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
		"5511999999999":                 "5511999999999@s.whatsapp.net",
		"+55 (11) 99999-9999":           "5511999999999@s.whatsapp.net",
		"+55.11.99999.9999":             "5511999999999@s.whatsapp.net",
		"5511999999999@s.whatsapp.net":  "5511999999999@s.whatsapp.net",
		"12345678":                      "12345678@s.whatsapp.net",
		"120363000000000000@g.us":       "120363000000000000@g.us",
		"5511999999999-1600000000@g.us": "5511999999999-1600000000@g.us",
	}
	for input, want := range valid {
		if got, err := NormalizeRecipient(input); err != nil || got != want {
			t.Errorf("NormalizeRecipient(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"123@g.us", "-120363000000@g.us", "12036300abc0@g.us", "123456789@lid", "120363000000000000@newsletter", "status@broadcast",
		"5511999999999@example.com", "+55@s.whatsapp.net", "abc5511999999", "55119999x9999", "1234567", "1234567890123456", "", "@s.whatsapp.net",
	} {
		if _, err := NormalizeRecipient(input); !errors.Is(err, ErrInvalidRecipient) {
			t.Errorf("NormalizeRecipient(%q) error = %v, want ErrInvalidRecipient", input, err)
		}
	}
}

type fakeSender struct {
	calls                  int
	number, text, mentions string
	media                  uazapi.Media
	pix                    uazapi.Pix
	err                    error
}

func (f *fakeSender) SendText(_ context.Context, number, text, mentions string) (uazapi.SendTextResult, error) {
	f.calls++
	f.number, f.text, f.mentions = number, text, mentions
	return uazapi.SendTextResult{MessageID: "msg-1", Status: "Pending"}, f.err
}

func (f *fakeSender) SendMedia(_ context.Context, media uazapi.Media) (uazapi.SendTextResult, error) {
	f.calls++
	f.media = media
	return uazapi.SendTextResult{MessageID: "msg-1", Status: "Pending"}, f.err
}

func (f *fakeSender) SendPix(_ context.Context, pix uazapi.Pix) (uazapi.SendTextResult, error) {
	f.calls++
	f.pix = pix
	return uazapi.SendTextResult{MessageID: "msg-1", Status: "Pending"}, f.err
}

func TestSendRejectsWithoutCallingSender(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, recipient, text string
		want                  error
	}{
		{"lid", "123456789@lid", "hi", ErrInvalidRecipient},
		{"empty text", "5511999999999", "", ErrInvalidText},
		{"whitespace text", "5511999999999", " \n\t", ErrInvalidText},
		{"placeholder", "5511999999999", "Oi {{name}}", ErrInvalidText},
		{"too long", "5511999999999", strings.Repeat("é", MaxTextRunes+1), ErrInvalidText},
	}
	for _, test := range tests {
		sender := &fakeSender{}
		if _, err := NewService(sender).Send(context.Background(), test.recipient, test.text, nil); !errors.Is(err, test.want) || sender.calls != 0 {
			t.Errorf("%s: error = %v, calls = %d", test.name, err, sender.calls)
		}
	}
}

func TestSendDeliversExactText(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{}
	text := "  Olá, tudo bem? {single}\n" + strings.Repeat("a", MaxTextRunes-27)
	result, err := NewService(sender).Send(context.Background(), "+55 11 99999-9999", text, nil)
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
	result, err := NewService(sender).Send(context.Background(), "5511999999999", "hi", nil)
	if !errors.Is(err, remote) || result.RecipientJID != "5511999999999@s.whatsapp.net" || result.MessageID != "" {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestSendMentions(t *testing.T) {
	t.Parallel()
	const group = "120363000000000000@g.us"
	valid := []struct {
		mentions []string
		want     string
	}{
		{nil, ""},
		{[]string{"+55 11 99999-9999", "5511888888888@s.whatsapp.net"}, "5511999999999,5511888888888"},
		{[]string{"ALL"}, "all"},
	}
	for _, test := range valid {
		sender := &fakeSender{}
		if _, err := NewService(sender).Send(context.Background(), group, "oi", test.mentions); err != nil || sender.mentions != test.want || sender.number != group {
			t.Errorf("mentions %v: sent %q to %q, error = %v", test.mentions, sender.mentions, sender.number, err)
		}
	}
	invalid := []struct {
		recipient string
		mentions  []string
	}{
		{"5511999999999", []string{"5511888888888"}},
		{group, []string{"all", "5511888888888"}},
		{group, []string{group}},
		{group, []string{"123@lid"}},
	}
	for _, test := range invalid {
		sender := &fakeSender{}
		if _, err := NewService(sender).Send(context.Background(), test.recipient, "oi", test.mentions); !errors.Is(err, ErrInvalidRecipient) || sender.calls != 0 {
			t.Errorf("%s %v: error = %v, calls = %d", test.recipient, test.mentions, err, sender.calls)
		}
	}
}

func TestSendMedia(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{}
	params := MediaParams{Type: "document", URL: "https://x.test/a.pdf", DocName: "Contrato.pdf", Caption: "segue", Mentions: []string{"all"}}
	if _, err := NewService(sender).SendMedia(context.Background(), "120363000000000000@g.us", params); err != nil {
		t.Fatal(err)
	}
	want := uazapi.Media{Number: "120363000000000000@g.us", Type: "document", File: "https://x.test/a.pdf", DocName: "Contrato.pdf", Mimetype: "application/pdf", Text: "segue", Mentions: "all"}
	if sender.media != want {
		t.Fatalf("media = %#v", sender.media)
	}
	for _, bad := range []MediaParams{
		{Type: "sticker", URL: "https://x.test/a.webp"},
		{Type: "document", URL: "file:///etc/passwd"},
		{Type: "document", URL: "/tmp/a.pdf"},
		{Type: "document", URL: "https://"},
		{Type: "image", URL: "https://x.test/a.jpg", DocName: "a.jpg"},
		{Type: "audio", URL: "https://x.test/a.mp3", Caption: "oi"},
		{Type: "image", URL: "https://x.test/a.jpg", Caption: "Oi {{name}}"},
	} {
		sender := &fakeSender{}
		if _, err := NewService(sender).SendMedia(context.Background(), "5511999999999", bad); !IsRejection(err) || sender.calls != 0 {
			t.Errorf("%#v: error = %v, calls = %d", bad, err, sender.calls)
		}
	}
}

func TestNormalizePixKey(t *testing.T) {
	t.Parallel()
	valid := []struct{ pixType, key, wantType, wantKey string }{
		{"cpf", "529.982.247-25", "CPF", "52998224725"},
		{"CNPJ", "11.222.333/0001-81", "CNPJ", "11222333000181"},
		{"CNPJ", "12.ABC.345/01DE-35", "CNPJ", "12ABC34501DE35"},
		{"phone", "+55 (11) 99999-9999", "PHONE", "+5511999999999"},
		{"EMAIL", "pix@empresa.com.br", "EMAIL", "pix@empresa.com.br"},
		{"evp", "123E4567-E89B-12D3-A456-426614174000", "EVP", "123e4567-e89b-12d3-a456-426614174000"},
	}
	for _, test := range valid {
		gotType, gotKey, err := NormalizePixKey(test.pixType, test.key)
		if err != nil || gotType != test.wantType || gotKey != test.wantKey {
			t.Errorf("NormalizePixKey(%q, %q) = %q, %q, %v", test.pixType, test.key, gotType, gotKey, err)
		}
	}
	for _, test := range []struct{ pixType, key string }{
		{"CPF", "529.982.247-24"}, {"CPF", "111.111.111-11"}, {"CPF", "5299822472"},
		{"CNPJ", "11.222.333/0001-80"}, {"CNPJ", "00000000000000"}, {"CNPJ", "12ABC34501DE3A"},
		{"PHONE", "+1 415 555 0100"}, {"PHONE", "11999999999"},
		{"EMAIL", "Fulano <pix@empresa.com>"}, {"EMAIL", "pix@"},
		{"EVP", "123e4567e89b12d3a456426614174000"},
		{"BOLETO", "123"},
	} {
		if _, _, err := NormalizePixKey(test.pixType, test.key); !errors.Is(err, ErrInvalidPix) {
			t.Errorf("NormalizePixKey(%q, %q) error = %v", test.pixType, test.key, err)
		}
	}
}

func TestSendPix(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{}
	if _, err := NewService(sender).SendPix(context.Background(), "5511999999999", PixParams{Type: "cpf", Key: "529.982.247-25", Name: " Leo "}); err != nil {
		t.Fatal(err)
	}
	if want := (uazapi.Pix{Number: "5511999999999@s.whatsapp.net", PixType: "CPF", PixKey: "52998224725", PixName: "Leo"}); sender.pix != want {
		t.Fatalf("pix = %#v", sender.pix)
	}
}

func TestMediaMimetype(t *testing.T) {
	t.Parallel()
	tests := []struct{ docName, url, want string }{
		{"Teste.pdf", "https://www.w3.org/x/dummy.pdf", "application/pdf"},
		{"", "https://x.test/files/Foto.PNG?sig=1", "image/png"},
		{"planilha.xlsx", "https://x.test/download?id=9", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"notas.txt", "", "text/plain"},
		{"semextensao", "https://x.test/download?id=9", ""},
		{"", "https://x.test/arquivo.desconhecido", ""},
	}
	for _, test := range tests {
		if got := mediaMimetype(test.docName, test.url); got != test.want {
			t.Errorf("mediaMimetype(%q, %q) = %q, want %q", test.docName, test.url, got, test.want)
		}
	}
}
