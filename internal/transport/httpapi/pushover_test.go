package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPushoverRoutesWithoutService(t *testing.T) {
	t.Parallel()
	handler := New(Options{APIToken: "api-token", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodPost} {
		path := "/api/v1/integrations/pushover"
		if method == http.MethodPost {
			path += "/send"
		}
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Authorization", "Bearer api-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d", method, path, response.Code)
		}
	}
}
