package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leofelipet/contexta/internal/trello"
)

func TestTrelloWebhookHeadRequiresSecret(t *testing.T) {
	t.Parallel()
	const secret = "a-separate-secret-that-is-at-least-32-characters"
	handler := New(Options{
		APIToken:            "api-token",
		Trello:              trello.NewService(nil, make([]byte, 32), nil, "https://context.example/webhooks/trello/"+secret),
		TrelloWebhookSecret: secret,
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	for path, wantStatus := range map[string]int{
		"/webhooks/trello/" + secret: http.StatusOK,
		"/webhooks/trello/wrong":     http.StatusUnauthorized,
	} {
		request := httptest.NewRequest(http.MethodHead, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("HEAD %s: status = %d, want %d", path, response.Code, wantStatus)
		}
	}
}
