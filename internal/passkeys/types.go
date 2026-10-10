package passkeys

import "time"

const MaxPasskeys = 10

type Passkey struct {
	ID         string    `json:"id"`
	PublicKey  string    `json:"public_key"`
	Counter    int64     `json:"counter"`
	Transports []string  `json:"transports,omitempty"`
	DeviceType string    `json:"device_type,omitempty"`
	BackedUp   bool      `json:"backed_up"`
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateParams struct {
	ID         string
	PublicKey  string
	Counter    int64
	Transports []string
	DeviceType string
	BackedUp   bool
	Name       string
}
