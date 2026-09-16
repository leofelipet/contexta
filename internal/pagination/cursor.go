package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

var ErrInvalidCursor = errors.New("invalid cursor")

type Cursor struct {
	Kind string    `json:"kind"`
	Time time.Time `json:"time,omitempty"`
	Text string    `json:"text,omitempty"`
	ID   string    `json:"id"`
}

func Encode(cursor Cursor) string {
	data, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func Decode(value, expectedKind string) (Cursor, error) {
	if value == "" {
		return Cursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	var cursor Cursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.ID == "" || cursor.Kind != expectedKind {
		return Cursor{}, ErrInvalidCursor
	}
	return cursor, nil
}
