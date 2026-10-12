package pushover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	emailcrypto "github.com/leofelipet/contexta/internal/email/crypto"
	provider "github.com/leofelipet/contexta/internal/providers/notifications/pushover"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotConfigured   = errors.New("pushover is not configured")
	ErrDisabled        = errors.New("pushover is disabled")
	ErrMissingKey      = emailcrypto.ErrMissingKey
	ErrProvider        = errors.New("pushover provider error")
)

type ProviderError struct{ Err error }

func (e *ProviderError) Error() string { return e.Err.Error() }
func (e *ProviderError) Unwrap() error { return e.Err }
func (e *ProviderError) Is(target error) bool {
	return target == ErrProvider
}

type Store interface {
	GetPushoverSettings(context.Context) (Settings, error)
	GetPushoverCredentials(context.Context) (Settings, []byte, []byte, error)
	UpsertPushoverSettings(context.Context, bool, []byte, []byte) (Settings, error)
}

type Client interface {
	Send(context.Context, provider.Credentials, provider.SendParams) (provider.Result, error)
}

type Service struct {
	store  Store
	key    []byte
	client Client
}

func NewService(store Store, credentialsKey []byte, client Client) *Service {
	if client == nil {
		client = provider.NewClient()
	}
	return &Service{store: store, key: credentialsKey, client: client}
}

func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	settings, err := s.store.GetPushoverSettings(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return Settings{}, nil
	}
	return settings, err
}

func (s *Service) Configure(ctx context.Context, params ConfigureParams) (Settings, error) {
	if err := s.requireKey(); err != nil {
		return Settings{}, err
	}
	settings, appCiphertext, userCiphertext, err := s.store.GetPushoverCredentials(ctx)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return Settings{}, err
	}

	var appToken, userKey string
	if errors.Is(err, ErrNotConfigured) {
		if params.AppToken == nil || params.UserKey == nil {
			return Settings{}, ErrInvalidArgument
		}
		appToken, userKey = strings.TrimSpace(*params.AppToken), strings.TrimSpace(*params.UserKey)
		settings.Enabled = true
	} else {
		app, decryptErr := emailcrypto.Decrypt(s.key, appCiphertext)
		if decryptErr != nil {
			return Settings{}, fmt.Errorf("decrypt pushover app token: %w", decryptErr)
		}
		user, decryptErr := emailcrypto.Decrypt(s.key, userCiphertext)
		if decryptErr != nil {
			return Settings{}, fmt.Errorf("decrypt pushover user key: %w", decryptErr)
		}
		appToken, userKey = string(app), string(user)
		if params.AppToken != nil && strings.TrimSpace(*params.AppToken) != "" {
			appToken = strings.TrimSpace(*params.AppToken)
		}
		if params.UserKey != nil && strings.TrimSpace(*params.UserKey) != "" {
			userKey = strings.TrimSpace(*params.UserKey)
		}
	}
	if appToken == "" || userKey == "" {
		return Settings{}, ErrInvalidArgument
	}
	if params.Enabled != nil {
		settings.Enabled = *params.Enabled
	}
	appCiphertext, err = emailcrypto.Encrypt(s.key, []byte(appToken))
	if err != nil {
		return Settings{}, err
	}
	userCiphertext, err = emailcrypto.Encrypt(s.key, []byte(userKey))
	if err != nil {
		return Settings{}, err
	}
	return s.store.UpsertPushoverSettings(ctx, settings.Enabled, appCiphertext, userCiphertext)
}

func (s *Service) Send(ctx context.Context, params SendParams) (SendResult, error) {
	message := strings.TrimSpace(params.Message)
	if message == "" {
		return SendResult{}, ErrInvalidArgument
	}
	priority := 0
	if params.Priority != nil {
		priority = *params.Priority
	}
	if priority < -2 || priority > 1 {
		return SendResult{}, ErrInvalidArgument
	}
	if err := s.requireKey(); err != nil {
		return SendResult{}, err
	}
	settings, appCiphertext, userCiphertext, err := s.store.GetPushoverCredentials(ctx)
	if err != nil {
		return SendResult{}, err
	}
	if !settings.Enabled {
		return SendResult{}, ErrDisabled
	}
	appToken, err := emailcrypto.Decrypt(s.key, appCiphertext)
	if err != nil {
		return SendResult{}, fmt.Errorf("decrypt pushover app token: %w", err)
	}
	userKey, err := emailcrypto.Decrypt(s.key, userCiphertext)
	if err != nil {
		return SendResult{}, fmt.Errorf("decrypt pushover user key: %w", err)
	}
	result, err := s.client.Send(ctx, provider.Credentials{AppToken: string(appToken), UserKey: string(userKey)}, provider.SendParams{
		Message: message, Title: strings.TrimSpace(params.Title), Priority: priority, URL: strings.TrimSpace(params.URL),
	})
	if err != nil {
		return SendResult{}, &ProviderError{Err: err}
	}
	return SendResult{Request: result.Request}, nil
}

func (s *Service) requireKey() error {
	if len(s.key) != 32 {
		return ErrMissingKey
	}
	return nil
}
