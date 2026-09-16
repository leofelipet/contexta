package contacts

import "time"

type Contact struct {
	ID                string    `json:"id"`
	ProviderContactID string    `json:"provider_contact_id"`
	Phone             string    `json:"phone,omitempty"`
	Name              string    `json:"name,omitempty"`
	PushName          string    `json:"push_name,omitempty"`
	ProfilePictureURL string    `json:"profile_picture_url,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ListParams struct {
	Query  string
	Limit  int
	Cursor string
}

type Page struct {
	Contacts   []Contact
	NextCursor string
}
