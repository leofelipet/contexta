package pagination

import (
	"errors"
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()
	want := Cursor{Kind: "messages", Time: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC), Text: "Alice", ID: "id-1"}
	got, err := Decode(Encode(want), "messages")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("cursor = %#v, want %#v", got, want)
	}
}

func TestInvalidCursor(t *testing.T) {
	t.Parallel()
	_, err := Decode("not-base64", "messages")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error = %v, want ErrInvalidCursor", err)
	}
}

func TestCursorKindCannotBeReused(t *testing.T) {
	t.Parallel()
	value := Encode(Cursor{Kind: "messages", ID: "id-1"})
	_, err := Decode(value, "contacts")
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("error = %v, want ErrInvalidCursor", err)
	}
}
