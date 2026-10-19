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
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/integrations/pushover"},
		{http.MethodPatch, "/api/v1/integrations/pushover"},
		{http.MethodGet, "/api/v1/integrations/pushover/sounds"},
		{http.MethodPost, "/api/v1/integrations/pushover/send"},
	} {
		method, path := route.method, route.path
		request := httptest.NewRequest(method, path, nil)
		request.Header.Set("Authorization", "Bearer api-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d", method, path, response.Code)
		}
	}
}
