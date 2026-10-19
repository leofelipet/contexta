package pushover

import (
	"context"
	"errors"
	"strings"
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
	soundsToken string
	err         error
}

func (f *fakeClient) Sounds(_ context.Context, appToken string) ([]provider.Sound, error) {
	f.soundsToken = appToken
	if f.err != nil {
		return nil, f.err
	}
	return []provider.Sound{{ID: "siren", Name: "Siren"}}, nil
}

func (f *fakeClient) Send(_ context.Context, credentials provider.Credentials, params provider.SendParams) (provider.Result, error) {
	f.credentials, f.params = credentials, params
	if f.err != nil {
		return provider.Result{}, f.err
	}
	result := provider.Result{Request: "request-id"}
	if params.Priority == 2 {
		result.Receipt = "receipt-id"
	}
	return result, nil
}

func newConfiguredService(t *testing.T, client Client) *Service {
	t.Helper()
	key := make([]byte, 32)
	app, err := emailcrypto.Encrypt(key, []byte("app"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := emailcrypto.Encrypt(key, []byte("user"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(&fakeStore{settings: Settings{Configured: true, Enabled: true}, app: app, user: user}, key, client)
}

func intPtr(value int) *int { return &value }

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
	for name, params := range map[string]SendParams{
		"missing message":            {},
		"priority below range":       {Message: "hello", Priority: intPtr(-3)},
		"priority above range":       {Message: "hello", Priority: intPtr(3)},
		"retry below minimum":        {Message: "hello", Priority: intPtr(2), Retry: intPtr(29)},
		"expire above maximum":       {Message: "hello", Priority: intPtr(2), Expire: intPtr(10801)},
		"expire zero":                {Message: "hello", Priority: intPtr(2), Expire: intPtr(0)},
		"expire below retry":         {Message: "hello", Priority: intPtr(2), Retry: intPtr(120), Expire: intPtr(60)},
		"default expire below retry": {Message: "hello", Priority: intPtr(2), Retry: intPtr(7200), Expire: nil},
		"retry without emergency":    {Message: "hello", Priority: intPtr(1), Retry: intPtr(60)},
		"expire without emergency":   {Message: "hello", Expire: intPtr(3600)},
		"sound with spaces":          {Message: "hello", Sound: "space alarm"},
		"sound too long":             {Message: "hello", Sound: strings.Repeat("a", 65)},
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

func TestSendEmergencyPriorityUsesDefaultsAndReturnsReceipt(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	service := newConfiguredService(t, client)
	result, err := service.Send(context.Background(), SendParams{Message: "hello", Priority: intPtr(2)})
	if err != nil || result.Request != "request-id" || result.Receipt != "receipt-id" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if client.params.Priority != 2 || client.params.Retry != DefaultEmergencyRetry || client.params.Expire != DefaultEmergencyExpire {
		t.Fatalf("params = %+v", client.params)
	}
}

func TestSendEmergencyPriorityUsesCustomTiming(t *testing.T) {
	t.Parallel()
	for name, timing := range map[string][2]int{"custom": {45, 900}, "minimum retry and maximum expire": {30, 10800}, "expire equals retry": {30, 30}} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{}
			service := newConfiguredService(t, client)
			result, err := service.Send(context.Background(), SendParams{Message: "hello", Priority: intPtr(2), Retry: intPtr(timing[0]), Expire: intPtr(timing[1])})
			if err != nil || result.Receipt != "receipt-id" {
				t.Fatalf("result = %+v, err = %v", result, err)
			}
			if client.params.Retry != timing[0] || client.params.Expire != timing[1] {
				t.Fatalf("params = %+v", client.params)
			}
		})
	}
}

func TestSendNonEmergencyPrioritiesUnchanged(t *testing.T) {
	t.Parallel()
	for _, priority := range []*int{nil, intPtr(-2), intPtr(-1), intPtr(0), intPtr(1)} {
		client := &fakeClient{}
		service := newConfiguredService(t, client)
		result, err := service.Send(context.Background(), SendParams{Message: "hello", Priority: priority})
		if err != nil || result.Request != "request-id" || result.Receipt != "" {
			t.Fatalf("priority %v: result = %+v, err = %v", priority, result, err)
		}
		if client.params.Retry != 0 || client.params.Expire != 0 {
			t.Fatalf("priority %v: params = %+v", priority, client.params)
		}
	}
}

func TestSendPassesTrimmedSound(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{"": "", " siren ": "siren", "custom_Sound-1": "custom_Sound-1"} {
		client := &fakeClient{}
		service := newConfiguredService(t, client)
		if _, err := service.Send(context.Background(), SendParams{Message: "hello", Sound: input}); err != nil {
			t.Fatalf("sound %q: err = %v", input, err)
		}
		if client.params.Sound != want {
			t.Fatalf("sound %q: params.Sound = %q, want %q", input, client.params.Sound, want)
		}
	}
}

func TestSoundsUsesDecryptedAppToken(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	sounds, err := newConfiguredService(t, client).Sounds(context.Background())
	if err != nil || len(sounds) != 1 || sounds[0].ID != "siren" || client.soundsToken != "app" {
		t.Fatalf("sounds = %+v, token = %q, err = %v", sounds, client.soundsToken, err)
	}
}

func TestSoundsErrors(t *testing.T) {
	t.Parallel()
	_, err := NewService(&fakeStore{}, make([]byte, 32), &fakeClient{}).Sounds(context.Background())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("not configured err = %v", err)
	}
	_, err = newConfiguredService(t, &fakeClient{err: errors.New("unavailable")}).Sounds(context.Background())
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("provider err = %v", err)
	}
}
