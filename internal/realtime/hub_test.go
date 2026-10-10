package realtime

import (
	"testing"
	"time"
)

func TestHubPublishReachesSubscriber(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ch, cancel := hub.Subscribe()
	defer cancel()
	hub.Publish()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive publish")
	}
}

func TestHubPublishDoesNotBlockWithoutSubscriber(t *testing.T) {
	t.Parallel()
	NewHub().Publish()
}
