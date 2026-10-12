package pushover

import "time"

type Settings struct {
	Configured bool      `json:"configured"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

type ConfigureParams struct {
	AppToken *string
	UserKey  *string
	Enabled  *bool
}

type SendParams struct {
	Message  string
	Title    string
	Priority *int
	URL      string
}

type SendResult struct {
	Request string `json:"request,omitempty"`
}
