//go:build windows

package cli

import "context"

func rendererInputSupported(renderer *runRenderer) bool {
	return false
}

func watchRendererInput(ctx context.Context, renderer *runRenderer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errRendererInputUnavailable
}
