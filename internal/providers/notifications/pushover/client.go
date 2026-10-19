package pushover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const endpoint = "https://api.pushover.net/1/messages.json"

type Credentials struct {
	AppToken string
	UserKey  string
}

type SendParams struct {
	Message  string
	Title    string
	Priority int
	URL      string
	// Retry and Expire are in seconds and only sent for emergency priority 2.
	Retry  int
	Expire int
}

type Result struct {
	Request string
	// Receipt is returned only for emergency priority notifications.
	Receipt string
}

type Client struct {
	endpoint string
	http     *http.Client
}

func NewClient() *Client {
	return &Client{endpoint: endpoint, http: &http.Client{Timeout: 10 * time.Second}}
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
