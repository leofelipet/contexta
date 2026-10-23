package trello

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	emailcrypto "github.com/leofelipet/contexta/internal/email/crypto"
	provider "github.com/leofelipet/contexta/internal/providers/trello"
	"github.com/leofelipet/contexta/internal/tasks"
)

type fakeStore struct {
	settings Settings
	apiKey   []byte
	secret   []byte
	token    []byte
	sync     CardSync
}

func (f *fakeStore) GetTrelloSettings(context.Context) (Settings, error) {
	if !f.settings.Configured {
		return Settings{}, ErrNotConfigured
	}
	return f.settings, nil
}

func (f *fakeStore) GetTrelloCredentials(context.Context) (Settings, []byte, []byte, []byte, error) {
	if !f.settings.Configured {
		return Settings{}, nil, nil, nil, ErrNotConfigured
	}
	return f.settings, f.apiKey, f.secret, f.token, nil
}

func (f *fakeStore) UpsertTrelloSettings(_ context.Context, settings Settings, apiKey, secret, token []byte) (Settings, error) {
	settings.Configured = true
	f.settings, f.apiKey, f.secret, f.token = settings, apiKey, secret, token
	return settings, nil
}

func (f *fakeStore) SetTrelloWebhookID(_ context.Context, webhookID string) (Settings, error) {
	f.settings.WebhookID = webhookID
	return f.settings, nil
}

func (f *fakeStore) SyncTrelloCard(_ context.Context, sync CardSync) (tasks.Task, bool, error) {
	f.sync = sync
	return tasks.Task{ID: "42", Title: sync.Title, Status: sync.Status}, true, nil
}

type fakeClient struct {
	webhookCredentials provider.Credentials
	callbackURL        string
	boardID            string
	cardCredentials    provider.Credentials
	cardID             string
	card               provider.Card
	err                error
}

func (f *fakeClient) CreateWebhook(_ context.Context, credentials provider.Credentials, callbackURL, boardID string) (provider.Webhook, error) {
	f.webhookCredentials, f.callbackURL, f.boardID = credentials, callbackURL, boardID
	if f.err != nil {
		return provider.Webhook{}, f.err
	}
	return provider.Webhook{ID: "webhook-1"}, nil
}

func (f *fakeClient) GetCard(_ context.Context, credentials provider.Credentials, cardID string) (provider.Card, error) {
	f.cardCredentials, f.cardID = credentials, cardID
	if f.err != nil {
		return provider.Card{}, f.err
	}
	return f.card, nil
}

func TestConfigureEncryptsCredentialsAndCreatesWebhook(t *testing.T) {
	t.Parallel()
	store, client := &fakeStore{}, &fakeClient{}
	service := NewService(store, make([]byte, 32), client, "https://context.example/webhooks/trello/secret")
	apiKey, applicationSecret, token, boardID := "key", "app-secret", "token", "board"
	listStatuses := map[string]string{"todo": tasks.StatusPending}

	settings, err := service.Configure(context.Background(), ConfigureParams{
		APIKey: &apiKey, ApplicationSecret: &applicationSecret, Token: &token, BoardID: &boardID, ListStatuses: &listStatuses,
	})
	if err != nil || !settings.Configured || settings.WebhookID != "webhook-1" {
		t.Fatalf("settings = %+v, err = %v", settings, err)
	}
	if string(store.apiKey) == apiKey || string(store.secret) == applicationSecret || string(store.token) == token {
		t.Fatal("Trello credentials were not encrypted")
	}
	if client.webhookCredentials != (provider.Credentials{APIKey: apiKey, Token: token}) || client.boardID != boardID || client.callbackURL == "" {
		t.Fatalf("webhook = %+v, board = %q, callback = %q", client.webhookCredentials, client.boardID, client.callbackURL)
	}
}

func TestHandleWebhookVerifiesAndMapsCard(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	apiKey, err := emailcrypto.Encrypt(key, []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := emailcrypto.Encrypt(key, []byte("app-secret"))
	if err != nil {
		t.Fatal(err)
	}
	token, err := emailcrypto.Encrypt(key, []byte("token"))
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{
		settings: Settings{Configured: true, Enabled: true, BoardID: "board", ListStatuses: map[string]string{"doing": tasks.StatusInProgress}},
		apiKey:   apiKey, secret: secret, token: token,
	}
	dueAt := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	client := &fakeClient{card: provider.Card{ID: "card", Name: "Follow up", Description: "Call the customer", ListID: "doing", DueAt: &dueAt}}
	callbackURL := "https://context.example/webhooks/trello/secret"
	service := NewService(store, key, client, callbackURL)
	body := []byte(`{"action":{"date":"2026-10-20T12:00:00.000Z","data":{"card":{"id":"card"}}},"model":{"id":"board"}}`)

	result, err := service.HandleWebhook(context.Background(), body, webhookSignature(body, "app-secret", callbackURL))
	if err != nil || !result.Processed || result.Task.ID != "42" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if client.cardCredentials != (provider.Credentials{APIKey: "key", Token: "token"}) || client.cardID != "card" {
		t.Fatalf("card request = %+v, card id = %q", client.cardCredentials, client.cardID)
	}
	if store.sync.CardID != "card" || store.sync.Status != tasks.StatusInProgress || !store.sync.DueAt.Equal(dueAt) {
		t.Fatalf("sync = %+v", store.sync)
	}

	_, err = service.HandleWebhook(context.Background(), body, "not-a-signature")
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("invalid signature err = %v", err)
	}
}

func webhookSignature(body []byte, secret, callbackURL string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write(body)
	_, _ = mac.Write([]byte(callbackURL))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
