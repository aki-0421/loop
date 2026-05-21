//go:build windows

package cli

import (
	"context"
	"errors"
	"time"
)

var errSleepKeypressUnsupported = errors.New("sleep keypress polling is unsupported on windows")

func waitForSleepKeypress(ctx context.Context, d time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, errSleepKeypressUnsupported
}
