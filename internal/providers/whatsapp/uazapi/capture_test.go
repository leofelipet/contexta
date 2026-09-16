package uazapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRedactsCredentialsAndURLs(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	payload := []byte(`{"token":"private","password":"private","fileURL":"https://signed.example/file","text":"hello"}`)
	if err := Capture(directory, payload); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, error = %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "private") || strings.Contains(content, "signed.example") {
		t.Fatalf("capture contains sensitive value: %s", content)
	}
	if !strings.Contains(content, "hello") {
		t.Fatalf("capture lost message structure: %s", content)
	}
}
