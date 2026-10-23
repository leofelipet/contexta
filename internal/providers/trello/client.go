package trello

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://api.trello.com/1"

type Credentials struct {
	APIKey string
	Token  string
}

type Webhook struct {
	ID string `json:"id"`
}

type Card struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"desc"`
	DueAt       *time.Time `json:"due"`
	Closed      bool       `json:"closed"`
	ListID      string     `json:"idList"`
	URL         string     `json:"url"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient() *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) CreateWebhook(ctx context.Context, credentials Credentials, callbackURL, boardID string) (Webhook, error) {
	query := url.Values{
		"callbackURL": {callbackURL},
		"idModel":     {boardID},
		"key":         {credentials.APIKey},
		"token":       {credentials.Token},
		"description": {"Contexta board sync"},
	}
	var webhook Webhook
	if err := c.request(ctx, http.MethodPost, "/webhooks/?"+query.Encode(), &webhook); err != nil {
		return Webhook{}, err
	}
	if webhook.ID == "" {
		return Webhook{}, fmt.Errorf("trello: webhook response did not include an id")
	}
	return webhook, nil
}

func (c *Client) GetCard(ctx context.Context, credentials Credentials, id string) (Card, error) {
	query := url.Values{
		"key":    {credentials.APIKey},
		"token":  {credentials.Token},
		"fields": {"id,name,desc,due,closed,idList,url"},
	}
	var card Card
	if err := c.request(ctx, http.MethodGet, "/cards/"+url.PathEscape(id)+"?"+query.Encode(), &card); err != nil {
		return Card{}, err
	}
	if card.ID == "" {
		return Card{}, fmt.Errorf("trello: card response did not include an id")
	}
	return card, nil
}

func (c *Client) request(ctx context.Context, method, path string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var errorPayload struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &errorPayload)
		if errorPayload.Message == "" {
			errorPayload.Message = response.Status
		}
		return fmt.Errorf("trello: %s", errorPayload.Message)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
