package pushover

import (
	"context"
	"errors"
	"testing"

	emailcrypto "github.com/leofelipet/contexta/internal/email/crypto"
	provider "github.com/leofelipet/contexta/internal/providers/notifications/pushover"
)

type fakeStore struct {
	settings Settings
	app      []byte
	user     []byte
}

func (f *fakeStore) GetPushoverSettings(context.Context) (Settings, error) {
	if !f.settings.Configured {
		return Settings{}, ErrNotConfigured
	}
	return f.settings, nil
}
func (f *fakeStore) GetPushoverCredentials(context.Context) (Settings, []byte, []byte, error) {
	if !f.settings.Configured {
		return Settings{}, nil, nil, ErrNotConfigured
	}
	return f.settings, f.app, f.user, nil
}
func (f *fakeStore) UpsertPushoverSettings(_ context.Context, enabled bool, app, user []byte) (Settings, error) {
	f.settings = Settings{Configured: true, Enabled: enabled}
	f.app, f.user = app, user
	return f.settings, nil
}

type fakeClient struct {
	credentials provider.Credentials
	params      provider.SendParams
	err         error
}

func (f *fakeClient) Send(_ context.Context, credentials provider.Credentials, params provider.SendParams) (provider.Result, error) {
	f.credentials, f.params = credentials, params
	if f.err != nil {
		return provider.Result{}, f.err
	}
	return provider.Result{Request: "request-id"}, nil
}

func TestConfigureEncryptsAndSendDecryptsCredentials(t *testing.T) {
	t.Parallel()
	store, client := &fakeStore{}, &fakeClient{}
	service := NewService(store, make([]byte, 32), client)
	app, user := "app-token", "user-key"
	settings, err := service.Configure(context.Background(), ConfigureParams{AppToken: &app, UserKey: &user})
	if err != nil || !settings.Configured || !settings.Enabled {
		t.Fatalf("settings = %+v, err = %v", settings, err)
	}
	if string(store.app) == app || string(store.user) == user {
		t.Fatal("credentials were not encrypted")
	}
	priority := 1
	result, err := service.Send(context.Background(), SendParams{Message: "hello", Title: "Contexta", Priority: &priority, URL: "https://example.com"})
	if err != nil || result.Request != "request-id" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if client.credentials != (provider.Credentials{AppToken: app, UserKey: user}) || client.params.Message != "hello" || client.params.Priority != 1 {
		t.Fatalf("credentials = %+v, params = %+v", client.credentials, client.params)
	}
}

func TestSendRejectsInvalidNotification(t *testing.T) {
	t.Parallel()
	service := NewService(&fakeStore{}, make([]byte, 32), &fakeClient{})
	priority := 2
	for name, params := range map[string]SendParams{
		"missing message":    {},
		"emergency priority": {Message: "hello", Priority: &priority},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Send(context.Background(), params)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestSendWrapsProviderError(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	app, err := emailcrypto.Encrypt(key, []byte("app"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := emailcrypto.Encrypt(key, []byte("user"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeStore{settings: Settings{Configured: true, Enabled: true}, app: app, user: user}, key, &fakeClient{err: errors.New("unavailable")})
	_, err = service.Send(context.Background(), SendParams{Message: "hello"})
	if !errors.Is(err, ErrProvider) || err.Error() != "unavailable" {
		t.Fatalf("err = %v", err)
	}
}
