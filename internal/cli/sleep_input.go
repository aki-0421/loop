package cli

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/term"
)

const sleepInputPollInterval = 200 * time.Millisecond

func waitForGitHubSleepPoll(ctx context.Context, d time.Duration, renderer *runRenderer) error {
	if !sleepKeypressEnabled(renderer) {
		return githubSleepPoll(ctx, d)
	}
	pressed, err := waitForSleepKeypress(ctx, d)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return githubSleepPoll(ctx, d)
	}
	if pressed && renderer != nil {
		renderer.SleepFetchRequested()
	}
	return nil
}

func sleepKeypressEnabled(renderer *runRenderer) bool {
	if renderer == nil || !renderer.enabled || !renderer.interactive {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}
