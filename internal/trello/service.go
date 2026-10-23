package trello

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" // Trello webhook signatures use HMAC-SHA1.
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	emailcrypto "github.com/leofelipet/contexta/internal/email/crypto"
	provider "github.com/leofelipet/contexta/internal/providers/trello"
	"github.com/leofelipet/contexta/internal/tasks"
)

var (
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrNotConfigured      = errors.New("trello is not configured")
	ErrDisabled           = errors.New("trello is disabled")
	ErrMissingKey         = emailcrypto.ErrMissingKey
	ErrProvider           = errors.New("trello provider error")
	ErrInvalidSignature   = errors.New("invalid Trello webhook signature")
	ErrWebhookUnavailable = errors.New("Trello webhook public URL is not configured")
)

type ProviderError struct{ Err error }

func (e *ProviderError) Error() string { return e.Err.Error() }
func (e *ProviderError) Unwrap() error { return e.Err }
func (e *ProviderError) Is(target error) bool {
	return target == ErrProvider
}

type Store interface {
	GetTrelloSettings(context.Context) (Settings, error)
	GetTrelloCredentials(context.Context) (Settings, []byte, []byte, []byte, error)
	UpsertTrelloSettings(context.Context, Settings, []byte, []byte, []byte) (Settings, error)
	SetTrelloWebhookID(context.Context, string) (Settings, error)
	SyncTrelloCard(context.Context, CardSync) (tasks.Task, bool, error)
}

type Client interface {
	CreateWebhook(context.Context, provider.Credentials, string, string) (provider.Webhook, error)
	GetCard(context.Context, provider.Credentials, string) (provider.Card, error)
}

type Service struct {
	store       Store
	key         []byte
	client      Client
	callbackURL string
}

func NewService(store Store, credentialsKey []byte, client Client, callbackURL string) *Service {
	if client == nil {
		client = provider.NewClient()
	}
	return &Service{store: store, key: credentialsKey, client: client, callbackURL: strings.TrimSpace(callbackURL)}
}

func (s *Service) GetSettings(ctx context.Context) (Settings, error) {
	settings, err := s.store.GetTrelloSettings(ctx)
	if errors.Is(err, ErrNotConfigured) {
		return Settings{}, nil
	}
	return settings, err
}

func (s *Service) Configure(ctx context.Context, params ConfigureParams) (Settings, error) {
	if err := s.requireKey(); err != nil {
		return Settings{}, err
	}
	if s.callbackURL == "" {
		return Settings{}, ErrWebhookUnavailable
	}

	settings, apiKeyCiphertext, secretCiphertext, tokenCiphertext, err := s.store.GetTrelloCredentials(ctx)
	newSettings := errors.Is(err, ErrNotConfigured)
	if err != nil && !newSettings {
		return Settings{}, err
	}

	var apiKey, applicationSecret, token string
	if newSettings {
		if params.APIKey == nil || params.ApplicationSecret == nil || params.Token == nil || params.BoardID == nil || params.ListStatuses == nil {
			return Settings{}, ErrInvalidArgument
		}
		apiKey = strings.TrimSpace(*params.APIKey)
		applicationSecret = strings.TrimSpace(*params.ApplicationSecret)
		token = strings.TrimSpace(*params.Token)
		settings.BoardID = strings.TrimSpace(*params.BoardID)
		settings.ListStatuses = *params.ListStatuses
		settings.Enabled = true
	} else {
		apiKey, err = decrypt(s.key, apiKeyCiphertext, "Trello API key")
		if err != nil {
			return Settings{}, err
		}
		applicationSecret, err = decrypt(s.key, secretCiphertext, "Trello application secret")
		if err != nil {
			return Settings{}, err
		}
		token, err = decrypt(s.key, tokenCiphertext, "Trello token")
		if err != nil {
			return Settings{}, err
		}
		if params.APIKey != nil {
			apiKey = strings.TrimSpace(*params.APIKey)
		}
		if params.ApplicationSecret != nil {
			applicationSecret = strings.TrimSpace(*params.ApplicationSecret)
		}
		if params.Token != nil {
			token = strings.TrimSpace(*params.Token)
		}
		if params.BoardID != nil {
			boardID := strings.TrimSpace(*params.BoardID)
			if settings.WebhookID != "" && boardID != settings.BoardID {
				return Settings{}, ErrInvalidArgument
			}
			settings.BoardID = boardID
		}
		if params.ListStatuses != nil {
			settings.ListStatuses = *params.ListStatuses
		}
	}
	if params.Enabled != nil {
		settings.Enabled = *params.Enabled
	}
	if apiKey == "" || applicationSecret == "" || token == "" || settings.BoardID == "" {
		return Settings{}, ErrInvalidArgument
	}
	listStatuses, err := normalizeListStatuses(settings.ListStatuses)
	if err != nil {
		return Settings{}, err
	}
	settings.ListStatuses = listStatuses

	apiKeyCiphertext, err = emailcrypto.Encrypt(s.key, []byte(apiKey))
	if err != nil {
		return Settings{}, err
	}
	secretCiphertext, err = emailcrypto.Encrypt(s.key, []byte(applicationSecret))
	if err != nil {
		return Settings{}, err
	}
	tokenCiphertext, err = emailcrypto.Encrypt(s.key, []byte(token))
	if err != nil {
		return Settings{}, err
	}
	settings, err = s.store.UpsertTrelloSettings(ctx, settings, apiKeyCiphertext, secretCiphertext, tokenCiphertext)
	if err != nil {
		return Settings{}, err
	}
	if settings.WebhookID != "" {
		return settings, nil
	}

	webhook, err := s.client.CreateWebhook(ctx, provider.Credentials{APIKey: apiKey, Token: token}, s.callbackURL, settings.BoardID)
	if err != nil {
		return Settings{}, &ProviderError{Err: err}
	}
	return s.store.SetTrelloWebhookID(ctx, webhook.ID)
}

func (s *Service) HandleWebhook(ctx context.Context, body []byte, signature string) (WebhookResult, error) {
	if err := s.requireKey(); err != nil {
		return WebhookResult{}, err
	}
	settings, apiKeyCiphertext, secretCiphertext, tokenCiphertext, err := s.store.GetTrelloCredentials(ctx)
	if err != nil {
		return WebhookResult{}, err
	}
	applicationSecret, err := decrypt(s.key, secretCiphertext, "Trello application secret")
	if err != nil {
		return WebhookResult{}, err
	}
	if !VerifySignature(body, applicationSecret, s.callbackURL, signature) {
		return WebhookResult{}, ErrInvalidSignature
	}
	if !settings.Enabled {
		return WebhookResult{}, nil
	}

	var event webhookEvent
	if err := json.Unmarshal(body, &event); err != nil {
		return WebhookResult{}, ErrInvalidArgument
	}
	if event.Action.Data.Card.ID == "" || event.Model.ID != settings.BoardID {
		return WebhookResult{}, nil
	}
	if event.Action.Date.IsZero() {
		return WebhookResult{}, ErrInvalidArgument
	}
	apiKey, err := decrypt(s.key, apiKeyCiphertext, "Trello API key")
	if err != nil {
		return WebhookResult{}, err
	}
	token, err := decrypt(s.key, tokenCiphertext, "Trello token")
	if err != nil {
		return WebhookResult{}, err
	}
	card, err := s.client.GetCard(ctx, provider.Credentials{APIKey: apiKey, Token: token}, event.Action.Data.Card.ID)
	if err != nil {
		return WebhookResult{}, &ProviderError{Err: err}
	}

	status, ok := settings.ListStatuses[card.ListID]
	if card.Closed {
		status = tasks.StatusCancelled
		ok = true
	}
	if !ok {
		return WebhookResult{}, nil
	}
	task, applied, err := s.store.SyncTrelloCard(ctx, CardSync{
		CardID: card.ID, Title: card.Name, Description: card.Description, Status: status, DueAt: card.DueAt, EventAt: event.Action.Date,
	})
	if err != nil {
		return WebhookResult{}, err
	}
	return WebhookResult{Processed: applied, Task: task}, nil
}

// VerifySignature validates the base64 HMAC-SHA1 Trello signature for body+callbackURL.
func VerifySignature(body []byte, applicationSecret, callbackURL, signature string) bool {
	provided, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha1.New, []byte(applicationSecret))
	_, _ = mac.Write(body)
	_, _ = mac.Write([]byte(callbackURL))
	return hmac.Equal(provided, mac.Sum(nil))
}

type webhookEvent struct {
	Action struct {
		Date time.Time `json:"date"`
		Data struct {
			Card struct {
				ID string `json:"id"`
			} `json:"card"`
		} `json:"data"`
	} `json:"action"`
	Model struct {
		ID string `json:"id"`
	} `json:"model"`
}

func normalizeListStatuses(values map[string]string) (map[string]string, error) {
	if len(values) == 0 || len(values) > 100 {
		return nil, ErrInvalidArgument
	}
	normalized := make(map[string]string, len(values))
	for listID, status := range values {
		listID, status = strings.TrimSpace(listID), strings.TrimSpace(status)
		if listID == "" || len(listID) > 256 || !tasks.ValidStatus(status) {
			return nil, ErrInvalidArgument
		}
		normalized[listID] = status
	}
	return normalized, nil
}

func decrypt(key, ciphertext []byte, name string) (string, error) {
	value, err := emailcrypto.Decrypt(key, ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt %s: %w", name, err)
	}
	return string(value), nil
}

func (s *Service) requireKey() error {
	if len(s.key) != 32 {
		return ErrMissingKey
	}
	return nil
}
