package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseDateTimeDateEndIsExclusiveNextDay(t *testing.T) {
	t.Parallel()
	got, err := parseDateTime("2026-09-15", true)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("time = %v, want %v", got, want)
	}
}

func TestAuthenticationAndMalformedWebhook(t *testing.T) {
	t.Parallel()
	const secret = "a-secret-that-must-never-appear-in-logs"
	var logs bytes.Buffer
	handler := New(Options{
		APIToken: "api-token", WebhookSecret: secret,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("API status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/webhooks/uazapi/wrong-secret", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("webhook auth status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/webhooks/uazapi/"+secret, strings.NewReader(`{"EventType":"messages","data":{}}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("malformed webhook status = %d, body = %s", response.Code, body)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatal("webhook secret appeared in logs")
	}
}
