package groq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leofelipet/contexta/internal/transcription"
)

func TestTranscribe(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer groq-key" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if r.FormValue("model") != "whisper-large-v3-turbo" || r.FormValue("language") != "pt" {
			t.Fatalf("form = %#v", r.Form)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":" mensagem transcrita ","language":"pt"}`))
	}))
	defer server.Close()

	client := NewClient("groq-key", "whisper-large-v3-turbo")
	client.endpoint = server.URL
	result, err := client.Transcribe(context.Background(), transcription.Audio{
		Data: []byte("audio"), MIMEType: "audio/ogg", FileName: "audio.ogg",
	}, "pt")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "mensagem transcrita" || result.Language != "pt" {
		t.Fatalf("result = %#v", result)
	}
}
