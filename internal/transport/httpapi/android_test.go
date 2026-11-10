package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestAndroidUpdateAndApk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), []byte(`{"version_code":2,"version_name":"1.0.1","notes":"sse"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.apk"), []byte("apk-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{
		APIToken: "api-token", AndroidDistDir: dir,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/android/update", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/android/update", nil)
	request.Header.Set("Authorization", "Bearer api-token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", response.Code, response.Body.Bytes())
	}
	var update androidUpdate
	if err := json.Unmarshal(response.Body.Bytes(), &update); err != nil {
		t.Fatal(err)
	}
	if update.VersionCode != 2 || update.VersionName != "1.0.1" || update.Notes != "sse" {
		t.Fatalf("update = %+v", update)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/android/apk", nil)
	request.Header.Set("Authorization", "Bearer api-token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("apk status = %d", response.Code)
	}
	if got := response.Body.String(); got != "apk-bytes" {
		t.Fatalf("apk body = %q", got)
	}
}

func TestAndroidUpdateDisabledOrInvalid(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	disabled := New(Options{APIToken: "api-token", Logger: logger})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/android/update", nil)
	request.Header.Set("Authorization", "Bearer api-token")
	response := httptest.NewRecorder()
	disabled.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d", response.Code)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), []byte(`{"version_code":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := New(Options{APIToken: "api-token", AndroidDistDir: dir, Logger: logger})
	request = httptest.NewRequest(http.MethodGet, "/api/v1/android/update", nil)
	request.Header.Set("Authorization", "Bearer api-token")
	response = httptest.NewRecorder()
	invalid.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("invalid metadata status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/android/apk", nil)
	request.Header.Set("Authorization", "Bearer api-token")
	response = httptest.NewRecorder()
	invalid.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing apk status = %d", response.Code)
	}
}
