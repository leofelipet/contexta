package admin

import "time"

type Dashboard struct {
	Contacts      int64      `json:"contacts"`
	Conversations int64      `json:"conversations"`
	Messages      int64      `json:"messages"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
	LastWebhookAt *time.Time `json:"last_webhook_at,omitempty"`
	UAZAPIStatus  string     `json:"uazapi_status"`
	Version       string     `json:"version"`
}

type MCPStatus struct {
	Enabled        bool       `json:"enabled"`
	Endpoint       string     `json:"endpoint"`
	Authentication string     `json:"authentication"`
	Tools          []string   `json:"tools"`
	LastAccessAt   *time.Time `json:"last_access_at,omitempty"`
}

type SystemOverview struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Version     string         `json:"version"`
	StartedAt   time.Time      `json:"started_at"`
	Database    SystemDatabase `json:"database"`
	Pool        SystemPool     `json:"pool"`
	Queues      SystemQueues   `json:"queues"`
}

type SystemDatabase struct {
	SizeBytes        int64      `json:"size_bytes"`
	MessagesLast24H  int64      `json:"messages_last_24h"`
	LastMessageAt    *time.Time `json:"last_message_at,omitempty"`
	LastWebhookAt    *time.Time `json:"last_webhook_at,omitempty"`
	MigrationVersion *int64     `json:"migration_version,omitempty"`
	DenylistEntries  int64      `json:"denylist_entries"`
}

type SystemPool struct {
	MaxConnections      int32 `json:"max_connections"`
	TotalConnections    int32 `json:"total_connections"`
	IdleConnections     int32 `json:"idle_connections"`
	AcquiredConnections int32 `json:"acquired_connections"`
}

type SystemQueues struct {
	TranscriptionPending    int64 `json:"transcription_pending"`
	TranscriptionProcessing int64 `json:"transcription_processing"`
	TranscriptionRetry      int64 `json:"transcription_retry"`
	TranscriptionFailed     int64 `json:"transcription_failed"`
}
