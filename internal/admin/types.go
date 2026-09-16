package admin

import "time"

type Dashboard struct {
	Contacts      int64      `json:"contacts"`
	Conversations int64      `json:"conversations"`
	Messages      int64      `json:"messages"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	LastWebhookAt *time.Time `json:"last_webhook_at,omitempty"`
	UAZAPIStatus  string     `json:"uazapi_status"`
}

type MCPStatus struct {
	Enabled        bool       `json:"enabled"`
	Endpoint       string     `json:"endpoint"`
	Authentication string     `json:"authentication"`
	Tools          []string   `json:"tools"`
	LastAccessAt   *time.Time `json:"last_access_at,omitempty"`
}
