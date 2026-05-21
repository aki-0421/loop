package cli

import (
	"context"
	"testing"
	"time"
)

func TestWaitForGitHubSleepPollFallsBackWhenInputIsUnavailable(t *testing.T) {
	previousSleep := githubSleepPoll
	defer func() { githubSleepPoll = previousSleep }()
	called := false
	githubSleepPoll = func(ctx context.Context, d time.Duration) error {
		called = true
		if d != 5*time.Second {
			t.Fatalf("duration = %s, want 5s", d)
		}
		return nil
	}

	if err := waitForGitHubSleepPoll(context.Background(), 5*time.Second, nil); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected fallback sleep function to be called")
	}
}

func TestContainsInterruptByte(t *testing.T) {
	if !containsInterruptByte([]byte{'a', 0x03}) {
		t.Fatal("expected Ctrl+C byte to be detected")
	}
	if containsInterruptByte([]byte("abc")) {
		t.Fatal("did not expect normal input to be treated as interrupt")
	}
}
