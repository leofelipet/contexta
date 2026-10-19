package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateReminderRejectsInvalidSendAt(t *testing.T) {
	t.Parallel()
	handler := New(Options{APIToken: "api-token", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for body, want := range map[string]string{
		`{"message":"x","send_at":"amanhã 13h"}`:                              "send_at must be RFC3339",
		`{"message":"x","send_at":"2026-10-19T13:00","timezone":"Mars/Base"}`: "unknown timezone",
		`{"message":"x","send_at":"2026-10-19T13:00","unknown":true}`:         "invalid request body",
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/reminders", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer api-token")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), want) {
			t.Fatalf("%s: status = %d, body = %s", body, response.Code, response.Body.String())
		}
	}
}
