package httpapi

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leofelipet/contexta/internal/email"
)

func TestEmailRoutesWithoutService(t *testing.T) {
	t.Parallel()
	handler := New(Options{APIToken: "api-token", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/email-accounts", nil)
	request.Header.Set("Authorization", "Bearer api-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestCreateEmailAccountWithoutKey(t *testing.T) {
	t.Parallel()
	handler := New(Options{
		APIToken: "api-token", Email: email.NewService(nil, nil, nil, nil),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	body := `{"name":"Work","address":"me@example.com","username":"me","password":"x","imap_host":"imap.example.com","imap_port":993,"smtp_host":"smtp.example.com","smtp_port":465,"save_sent_copy":false}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/email-accounts", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer api-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHandleStoreErrorEmailMapping(t *testing.T) {
	t.Parallel()
	h := &handler{}
	cases := []struct {
		err  error
		want int
	}{
		{email.ErrNotFound, http.StatusNotFound},
		{email.ErrMessageNotFound, http.StatusNotFound},
		{email.ErrDisabled, http.StatusConflict},
		{email.ErrMissingKey, http.StatusServiceUnavailable},
		{email.ErrInvalidArgument, http.StatusBadRequest},
		{fmt.Errorf("wrapped: %w", &email.ProviderError{Protocol: "imap", Err: fmt.Errorf("login failed")}), http.StatusBadGateway},
	}
	for _, tc := range cases {
		response := httptest.NewRecorder()
		h.handleStoreError(response, tc.err)
		if response.Code != tc.want {
			t.Fatalf("%v: status = %d, want %d", tc.err, response.Code, tc.want)
		}
	}
}
