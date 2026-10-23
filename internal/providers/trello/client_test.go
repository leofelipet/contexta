package trello

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCreatesWebhookAndLoadsCard(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "key" || r.URL.Query().Get("token") != "token" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		switch r.URL.Path {
		case "/webhooks/":
			if r.Method != http.MethodPost || r.URL.Query().Get("idModel") != "board" || r.URL.Query().Get("callbackURL") != "https://context.example/webhooks/trello/secret" {
				t.Fatalf("webhook request = %s %s", r.Method, r.URL)
			}
			_, _ = w.Write([]byte(`{"id":"webhook"}`))
		case "/cards/card":
			if r.Method != http.MethodGet || r.URL.Query().Get("fields") != "id,name,desc,due,closed,idList,url" {
				t.Fatalf("card request = %s %s", r.Method, r.URL)
			}
			_, _ = w.Write([]byte(`{"id":"card","name":"Follow up","desc":"Call","idList":"todo"}`))
		default:
			t.Fatalf("path = %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, http: server.Client()}
	credentials := Credentials{APIKey: "key", Token: "token"}

	webhook, err := client.CreateWebhook(context.Background(), credentials, "https://context.example/webhooks/trello/secret", "board")
	if err != nil || webhook.ID != "webhook" {
		t.Fatalf("webhook = %+v, err = %v", webhook, err)
	}
	card, err := client.GetCard(context.Background(), credentials, "card")
	if err != nil || card.ID != "card" || card.ListID != "todo" {
		t.Fatalf("card = %+v, err = %v", card, err)
	}
}
