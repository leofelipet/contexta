package uazapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxCaptureBytes = 2 << 20
	maxCaptureFiles = 20
)

func Capture(directory string, payload []byte) error {
	if directory == "" {
		return nil
	}
	if len(payload) > maxCaptureBytes {
		return errors.New("capture payload exceeds 2 MiB")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create capture directory: %w", err)
	}
	redacted, err := redactPayload(payload)
	if err != nil {
		return fmt.Errorf("redact captured payload: %w", err)
	}
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return fmt.Errorf("create capture name: %w", err)
	}
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(random) + ".json"
	if err := os.WriteFile(filepath.Join(directory, name), redacted, 0o600); err != nil {
		return fmt.Errorf("write captured payload: %w", err)
	}
	return pruneCaptures(directory)
}

func redactPayload(payload []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	redact(value)
	return json.MarshalIndent(value, "", "  ")
}

func redact(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
				strings.Contains(lower, "authorization") || strings.Contains(lower, "cookie") ||
				strings.Contains(lower, "password") || strings.Contains(lower, "apikey") ||
				strings.Contains(lower, "api_key") || strings.Contains(lower, "signature") ||
				strings.Contains(lower, "session") || strings.HasSuffix(lower, "url") {
				current[key] = "[REDACTED]"
				continue
			}
			redact(child)
		}
	case []any:
		for _, child := range current {
			redact(child)
		}
	}
}

func pruneCaptures(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read capture directory: %w", err)
	}
	jsonFiles := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			jsonFiles = append(jsonFiles, entry)
		}
	}
	for _, entry := range jsonFiles[:max(0, len(jsonFiles)-maxCaptureFiles)] {
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return fmt.Errorf("remove old capture: %w", err)
		}
	}
	return nil
}
