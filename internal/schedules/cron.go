package schedules

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// TickInterval is how often the scheduler looks for due schedules. Cron
// expressions firing more often than this are rejected because they could
// never be honored.
const TickInterval = 5 * time.Minute

// DefaultTimezone applies when neither the schedule nor the server config sets one.
const DefaultTimezone = "America/Sao_Paulo"

var ErrInvalidCron = errors.New("invalid cron expression")

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Validate checks a 5-field cron expression (or descriptor such as @daily)
// and its timezone, rejecting schedules that fire more often than TickInterval.
func Validate(expression, timezone string) error {
	schedule, location, err := parse(expression, timezone)
	if err != nil {
		return err
	}
	// Sample a day of occurrences: enough to catch any sub-interval minute pattern.
	current := schedule.Next(time.Now().In(location))
	if current.IsZero() {
		return fmt.Errorf("%w: never fires", ErrInvalidCron)
	}
	limit := current.Add(24 * time.Hour)
	for current.Before(limit) {
		next := schedule.Next(current)
		if next.IsZero() {
			break
		}
		if next.Sub(current) < TickInterval {
			return fmt.Errorf("%w: fires more often than every %s", ErrInvalidCron, TickInterval)
		}
		current = next
	}
	return nil
}

// Next returns the first occurrence strictly after from, in UTC.
func Next(expression, timezone string, from time.Time) (time.Time, error) {
	schedule, location, err := parse(expression, timezone)
	if err != nil {
		return time.Time{}, err
	}
	next := schedule.Next(from.In(location))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("%w: never fires", ErrInvalidCron)
	}
	return next.UTC(), nil
}

func parse(expression, timezone string) (cron.Schedule, *time.Location, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" || strings.Contains(expression, "TZ=") || strings.HasPrefix(expression, "@every") {
		return nil, nil, ErrInvalidCron
	}
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil || timezone == "" {
		return nil, nil, fmt.Errorf("%w: unknown timezone", ErrInvalidCron)
	}
	schedule, err := parser.Parse(expression)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidCron, err)
	}
	return schedule, location, nil
}
