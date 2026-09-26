package admin

import "time"

type Dashboard struct {
	Contacts              int64             `json:"contacts"`
	Conversations         int64             `json:"conversations"`
	Messages              int64             `json:"messages"`
	MessagesInbound       int64             `json:"messages_inbound"`
	MessagesOutbound      int64             `json:"messages_outbound"`
	MessagesLast7D        int64             `json:"messages_last_7d"`
	MessagesLast30D       int64             `json:"messages_last_30d"`
	Groups                int64             `json:"groups"`
	Directs               int64             `json:"directs"`
	ActiveConversations7D int64             `json:"active_conversations_7d"`
	LastMessageAt         *time.Time        `json:"last_message_at,omitempty"`
	LastWebhookAt         *time.Time        `json:"last_webhook_at,omitempty"`
	UAZAPIStatus          string            `json:"uazapi_status"`
	Version               string            `json:"version"`
	Traffic               []DailyTraffic    `json:"traffic"`
	MessageTypes          []NamedCount      `json:"message_types"`
	TopConversations      []TopConversation `json:"top_conversations"`
}

type DailyTraffic struct {
	Date     string `json:"date"`
	Inbound  int64  `json:"inbound"`
	Outbound int64  `json:"outbound"`
}

type NamedCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type TopConversation struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	MessageCount int64  `json:"message_count"`
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
