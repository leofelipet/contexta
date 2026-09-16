package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const minimumSecretLength = 32

type Config struct {
	Environment     string
	DatabaseURL     string
	HTTPHost        string
	HTTPPort        int
	ShutdownTimeout time.Duration
	APIToken        string
	MCPToken        string
	MCPEnabled      bool
	UAZAPI          UAZAPI
	Transcription   Transcription
}

type UAZAPI struct {
	BaseURL          string
	Token            string
	InstanceID       string
	WebhookSecret    string
	WebhookPublicURL string
	CaptureDir       string
}

type Transcription struct {
	Enabled  bool
	APIKey   string
	Model    string
	Language string
}

func Load() (Config, error) {
	port, err := intEnv("HTTP_PORT", 8080)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := durationEnv("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	mcpEnabled, err := boolEnv("MCP_ENABLED", true)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Environment:     stringEnv("APP_ENV", "development"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		HTTPHost:        stringEnv("HTTP_HOST", "127.0.0.1"),
		HTTPPort:        port,
		ShutdownTimeout: shutdownTimeout,
		APIToken:        os.Getenv("API_BEARER_TOKEN"),
		MCPToken:        os.Getenv("MCP_BEARER_TOKEN"),
		MCPEnabled:      mcpEnabled,
		UAZAPI: UAZAPI{
			BaseURL:          strings.TrimRight(os.Getenv("UAZAPI_BASE_URL"), "/"),
			Token:            os.Getenv("UAZAPI_TOKEN"),
			InstanceID:       os.Getenv("UAZAPI_INSTANCE_ID"),
			WebhookSecret:    os.Getenv("UAZAPI_WEBHOOK_SECRET"),
			WebhookPublicURL: strings.TrimRight(os.Getenv("UAZAPI_WEBHOOK_PUBLIC_URL"), "/"),
			CaptureDir:       os.Getenv("UAZAPI_CAPTURE_DIR"),
		},
		Transcription: Transcription{
			Enabled:  false,
			APIKey:   os.Getenv("GROQ_API_KEY"),
			Model:    stringEnv("GROQ_TRANSCRIPTION_MODEL", "whisper-large-v3-turbo"),
			Language: stringEnv("GROQ_TRANSCRIPTION_LANGUAGE", "pt"),
		},
	}
	transcriptionEnabled, err := boolEnv("TRANSCRIPTION_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	cfg.Transcription.Enabled = transcriptionEnabled

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if net.ParseIP(c.HTTPHost) == nil && c.HTTPHost != "localhost" {
		errs = append(errs, fmt.Errorf("HTTP_HOST must be an IP address or localhost"))
	}
	if c.HTTPPort < 1 || c.HTTPPort > 65535 {
		errs = append(errs, errors.New("HTTP_PORT must be between 1 and 65535"))
	}
	if len(c.APIToken) < minimumSecretLength {
		errs = append(errs, fmt.Errorf("API_BEARER_TOKEN must contain at least %d characters", minimumSecretLength))
	}
	if c.MCPEnabled && len(c.MCPToken) < minimumSecretLength {
		errs = append(errs, fmt.Errorf("MCP_BEARER_TOKEN must contain at least %d characters", minimumSecretLength))
	}
	if c.UAZAPI.BaseURL == "" {
		errs = append(errs, errors.New("UAZAPI_BASE_URL is required"))
	}
	if c.UAZAPI.Token == "" {
		errs = append(errs, errors.New("UAZAPI_TOKEN is required"))
	}
	if c.UAZAPI.InstanceID == "" {
		errs = append(errs, errors.New("UAZAPI_INSTANCE_ID is required"))
	}
	if len(c.UAZAPI.WebhookSecret) < minimumSecretLength {
		errs = append(errs, fmt.Errorf("UAZAPI_WEBHOOK_SECRET must contain at least %d characters", minimumSecretLength))
	}
	if c.Transcription.Enabled && c.Transcription.APIKey == "" {
		errs = append(errs, errors.New("GROQ_API_KEY is required when transcription is enabled"))
	}
	if c.Transcription.Enabled && c.Transcription.Model != "whisper-large-v3-turbo" && c.Transcription.Model != "whisper-large-v3" {
		errs = append(errs, errors.New("GROQ_TRANSCRIPTION_MODEL must be whisper-large-v3-turbo or whisper-large-v3"))
	}
	if c.Transcription.Enabled && len(c.Transcription.Language) != 2 {
		errs = append(errs, errors.New("GROQ_TRANSCRIPTION_LANGUAGE must be an ISO-639-1 code"))
	}
	return errors.Join(errs...)
}

func (c Config) HTTPAddress() string {
	return net.JoinHostPort(c.HTTPHost, strconv.Itoa(c.HTTPPort))
}

func stringEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func intEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}

func boolEnv(key string, fallback bool) (bool, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}
