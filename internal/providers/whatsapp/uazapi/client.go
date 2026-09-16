package uazapi

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

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type InstanceStatus struct {
	Instance struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Status      string `json:"status"`
		ProfileName string `json:"profileName"`
	} `json:"instance"`
	Status struct {
		Connected bool `json:"connected"`
		LoggedIn  bool `json:"loggedIn"`
	} `json:"status"`
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) Status(ctx context.Context) (InstanceStatus, error) {
	var status InstanceStatus
	if err := c.do(ctx, http.MethodGet, "/instance/status", nil, &status); err != nil {
		return InstanceStatus{}, err
	}
	return status, nil
}

func (c *Client) ConfigureWebhook(ctx context.Context, callbackURL string) error {
	payload := map[string]any{
		"enabled":             true,
		"url":                 callbackURL,
		"events":              []string{"connection", "history", "messages", "messages_update"},
		"excludeMessages":     []string{},
		"addUrlEvents":        false,
		"addUrlTypesMessages": false,
	}
	return c.do(ctx, http.MethodPost, "/webhook", payload, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body any, output any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode UAZAPI request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create UAZAPI request: %w", err)
	}
	req.Header.Set("token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call UAZAPI: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("UAZAPI returned status %d", response.StatusCode)
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return fmt.Errorf("decode UAZAPI response: %w", err)
	}
	return nil
}
