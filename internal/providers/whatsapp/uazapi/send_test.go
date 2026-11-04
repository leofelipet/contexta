package uazapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leofelipet/contexta/internal/transcription"
)

func TestSendText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, response, wantID string
	}{
		{"messageid", `{"id":"r1","messageid":"ABC","chatid":"5511999999999@s.whatsapp.net","status":"Pending"}`, "ABC"},
		{"messageId fallback", `{"messageId":"XYZ","status":"Pending"}`, "XYZ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.Method != http.MethodPost || r.URL.Path != "/send/text" || r.Header.Get("token") != "instance-token" ||
					r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("request = %s %s token=%q type=%q", r.Method, r.URL.Path, r.Header.Get("token"), r.Header.Get("Content-Type"))
				}
				if want := `{"number":"5511999999999@s.whatsapp.net","text":"Olá  {x}\n"}`; string(body) != want {
					t.Errorf("body = %s, want %s", body, want)
				}
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			result, err := NewClient(server.URL, "instance-token").SendText(context.Background(), "5511999999999@s.whatsapp.net", "Olá  {x}\n", "")
			if err != nil || result.MessageID != test.wantID || result.Status != "Pending" {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
		})
	}
}

func TestSendTextRemoteError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"bad"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "instance-token").SendText(context.Background(), "5511999999999@s.whatsapp.net", "hi", "")
	var remote *transcription.RemoteError
	if !errors.As(err, &remote) || remote.StatusCode != http.StatusBadRequest {
		t.Fatalf("error = %v", err)
	}
}

func TestSendBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, want string
		send             func(*Client) (SendTextResult, error)
	}{
		{"text with mentions", "/send/text", `{"number":"1203@g.us","text":"oi @5511999999999","mentions":"5511999999999"}`, func(c *Client) (SendTextResult, error) {
			return c.SendText(context.Background(), "1203@g.us", "oi @5511999999999", "5511999999999")
		}},
		{"media", "/send/media", `{"number":"1203@g.us","type":"document","file":"https://x.test/a.pdf","docName":"a.pdf","text":"segue"}`, func(c *Client) (SendTextResult, error) {
			return c.SendMedia(context.Background(), Media{Number: "1203@g.us", Type: "document", File: "https://x.test/a.pdf", DocName: "a.pdf", Text: "segue"})
		}},
		{"pix", "/send/pix-button", `{"number":"5511999999999@s.whatsapp.net","pixType":"EVP","pixKey":"123e4567-e89b-12d3-a456-426614174000","pixName":"Loja"}`, func(c *Client) (SendTextResult, error) {
			return c.SendPix(context.Background(), Pix{Number: "5511999999999@s.whatsapp.net", PixType: "EVP", PixKey: "123e4567-e89b-12d3-a456-426614174000", PixName: "Loja"})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if r.URL.Path != test.path || string(body) != test.want {
					t.Errorf("request %s body = %s, want %s %s", r.URL.Path, body, test.path, test.want)
				}
				_, _ = w.Write([]byte(`{"messageid":"M1","status":"Pending"}`))
			}))
			defer server.Close()
			if result, err := test.send(NewClient(server.URL, "instance-token")); err != nil || result.MessageID != "M1" {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
		})
	}
}
