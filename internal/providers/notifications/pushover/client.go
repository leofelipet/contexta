package pushover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	endpoint       = "https://api.pushover.net/1/messages.json"
	soundsEndpoint = "https://api.pushover.net/1/sounds.json"
)

type Credentials struct {
	AppToken string
	UserKey  string
}

type SendParams struct {
	Message  string
	Title    string
	Priority int
	URL      string
	// Sound overrides the recipient's default tone; empty keeps the default.
	Sound string
	// Retry and Expire are in seconds and only sent for emergency priority 2.
	Retry  int
	Expire int
}

type Result struct {
	Request string
	// Receipt is returned only for emergency priority notifications.
	Receipt string
}

type Sound struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Client struct {
	endpoint       string
	soundsEndpoint string
	http           *http.Client
}

func NewClient() *Client {
	return &Client{endpoint: endpoint, soundsEndpoint: soundsEndpoint, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) Send(ctx context.Context, credentials Credentials, params SendParams) (Result, error) {
	form := url.Values{
		"token":   {credentials.AppToken},
		"user":    {credentials.UserKey},
		"message": {params.Message},
	}
	if params.Title != "" {
		form.Set("title", params.Title)
	}
	if params.Priority != 0 {
		form.Set("priority", strconv.Itoa(params.Priority))
	}
	if params.URL != "" {
		form.Set("url", params.URL)
	}
	if params.Sound != "" {
		form.Set("sound", params.Sound)
	}
	if params.Priority == 2 {
		form.Set("retry", strconv.Itoa(params.Retry))
		form.Set("expire", strconv.Itoa(params.Expire))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Result{}, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(request)
	if err != nil {
		return Result{}, fmt.Errorf("request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Result{}, fmt.Errorf("read response: %w", err)
	}
	var payload struct {
		Status  int      `json:"status"`
		Request string   `json:"request"`
		Receipt string   `json:"receipt"`
		Errors  []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Result{}, fmt.Errorf("decode response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || payload.Status != 1 {
		detail := strings.Join(payload.Errors, "; ")
		if detail == "" {
			detail = response.Status
		}
		return Result{}, fmt.Errorf("pushover: %s", detail)
	}
	return Result{Request: payload.Request, Receipt: payload.Receipt}, nil
}

// Sounds lists the built-in sounds plus custom sounds uploaded to the application owner's account.
func (c *Client) Sounds(ctx context.Context, appToken string) ([]Sound, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.soundsEndpoint+"?"+url.Values{"token": {appToken}}.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var payload struct {
		Status int               `json:"status"`
		Sounds map[string]string `json:"sounds"`
		Errors []string          `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || payload.Status != 1 {
		detail := strings.Join(payload.Errors, "; ")
		if detail == "" {
			detail = response.Status
		}
		return nil, fmt.Errorf("pushover: %s", detail)
	}
	sounds := make([]Sound, 0, len(payload.Sounds))
	for id, name := range payload.Sounds {
		sounds = append(sounds, Sound{ID: id, Name: name})
	}
	sort.Slice(sounds, func(i, j int) bool { return sounds[i].Name < sounds[j].Name })
	return sounds, nil
}
