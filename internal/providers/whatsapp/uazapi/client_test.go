package uazapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDownloadAudio(t *testing.T) {
	t.Parallel()
	audio := []byte("ogg-audio")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message/download" || r.Header.Get("token") != "instance-token" {
			t.Fatalf("request = %s, token = %q", r.URL.Path, r.Header.Get("token"))
		}
		var request struct {
			ID           string `json:"id"`
			ReturnBase64 bool   `json:"return_base64"`
			ReturnLink   bool   `json:"return_link"`
			GenerateMP3  bool   `json:"generate_mp3"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ID != "message-1" || !request.ReturnBase64 || request.ReturnLink || request.GenerateMP3 {
			t.Fatalf("download request = %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"base64Data":"` + base64.StdEncoding.EncodeToString(audio) + `","mimetype":"audio/ogg"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "instance-token")
	result, err := client.DownloadAudio(context.Background(), "message-1", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != string(audio) || result.MIMEType != "audio/ogg" {
		t.Fatalf("audio = %#v", result)
	}
}
