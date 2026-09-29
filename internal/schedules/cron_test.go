package schedules

import (
	"errors"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	valid := []string{"0 9 * * 1-5", "*/5 * * * *", "*/15 8-18 * * *", "30 7 1 * *", "@daily", "@weekly", "@hourly"}
	for _, expression := range valid {
		if err := Validate(expression, DefaultTimezone); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", expression, err)
		}
	}
	invalid := []string{"", "* * * * *", "*/2 * * * *", "0,3 * * * *", "*/7 * * * *", "0 9 * *", "@every 10m", "TZ=UTC 0 9 * * *", "61 * * * *"}
	for _, expression := range invalid {
		if err := Validate(expression, DefaultTimezone); !errors.Is(err, ErrInvalidCron) {
			t.Errorf("Validate(%q) = %v, want ErrInvalidCron", expression, err)
		}
	}
	if err := Validate("0 9 * * *", "Mars/Olympus"); !errors.Is(err, ErrInvalidCron) {
		t.Errorf("unknown timezone err = %v", err)
	}
	if err := Validate("0 9 * * *", ""); !errors.Is(err, ErrInvalidCron) {
		t.Errorf("empty timezone err = %v", err)
	}
}

func TestNextUsesTimezone(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC) // 08:00 in São Paulo (UTC-3)
	next, err := Next("0 9 * * *", DefaultTimezone, from)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC); !next.Equal(want) || next.Location() != time.UTC {
		t.Fatalf("next = %v, want %v", next, want)
	}

	// Strictly after: firing at 09:00 schedules tomorrow.
	next, err = Next("0 9 * * *", DefaultTimezone, time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestUntilNextTick(t *testing.T) {
	t.Parallel()
	cases := map[time.Time]time.Duration{
		time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC):  5 * time.Minute,
		time.Date(2026, 9, 29, 10, 3, 30, 0, time.UTC): 90 * time.Second,
		time.Date(2026, 9, 29, 10, 59, 0, 0, time.UTC): time.Minute,
	}
	for now, want := range cases {
		if got := untilNextTick(now); got != want {
			t.Errorf("untilNextTick(%v) = %v, want %v", now, got, want)
		}
	}
}
