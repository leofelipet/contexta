package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
}

type OpenRouter struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func NewOpenRouter(apiKey, baseURL, model string) *OpenRouter {
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	if model == "" {
		model = "openai/text-embedding-3-small"
	}
	return &OpenRouter{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{Timeout: 45 * time.Second},
	}
}

func (c *OpenRouter) Model() string { return c.model }

func (c *OpenRouter) Available() bool { return c != nil && c.apiKey != "" }

type embedRequest struct {
	Model      string `json:"model"`
	Input      any    `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *OpenRouter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if !c.Available() {
		return nil, fmt.Errorf("openrouter api key is not configured")
	}
	if len(texts) == 0 {
		return nil, fmt.Errorf("no texts to embed")
	}
	cleaned := make([]string, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("empty text cannot be embedded")
		}
		cleaned = append(cleaned, text)
	}

	var input any = cleaned[0]
	if len(cleaned) > 1 {
		input = cleaned
	}
	payload, err := json.Marshal(embedRequest{
		Model:      c.model,
		Input:      input,
		Dimensions: 1536,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openrouter embeddings request: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read openrouter embeddings response: %w", err)
	}
	var parsed embedResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode openrouter embeddings response: %w", err)
	}
	if res.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if parsed.Error != nil && parsed.Error.Message != "" {
			message = parsed.Error.Message
		}
		return nil, fmt.Errorf("openrouter embeddings status %d: %s", res.StatusCode, message)
	}
	if len(parsed.Data) != len(cleaned) {
		return nil, fmt.Errorf("openrouter returned %d embeddings for %d inputs", len(parsed.Data), len(cleaned))
	}

	out := make([][]float32, len(cleaned))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(out) {
			return nil, fmt.Errorf("openrouter returned invalid embedding index %d", item.Index)
		}
		if len(item.Embedding) != 1536 {
			return nil, fmt.Errorf("expected 1536-dim embedding, got %d", len(item.Embedding))
		}
		out[item.Index] = item.Embedding
	}
	for i, vector := range out {
		if len(vector) == 0 {
			return nil, fmt.Errorf("missing embedding at index %d", i)
		}
	}
	return out, nil
}
