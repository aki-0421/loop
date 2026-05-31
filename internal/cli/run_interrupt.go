package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

const interruptExitCode = 130

type runInterruptContextKey struct{}

type runInterruptState struct {
	gracefulCtx    context.Context
	cancelGraceful context.CancelFunc
	cancelForce    context.CancelFunc

	mu                  sync.Mutex
	gracefulRequested   bool
	gracefulCallbacks   []func()
	gracefulCallbackRun bool
}

func newRunInterruptContext(parent context.Context) (context.Context, *runInterruptState) {
	forceCtx, cancelForce := context.WithCancel(parent)
	gracefulCtx, cancelGraceful := context.WithCancel(parent)
	state := &runInterruptState{
		gracefulCtx:    gracefulCtx,
		cancelGraceful: cancelGraceful,
		cancelForce:    cancelForce,
	}
	ctx := context.WithValue(forceCtx, runInterruptContextKey{}, state)
	return ctx, state
}

func newSignalRunInterruptContext(parent context.Context, writer io.Writer) (context.Context, func()) {
	ctx, state := newRunInterruptContext(parent)
	signals := make(chan os.Signal, 2)
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-ctx.Done():
				return
			case <-signals:
				if !state.GracefulRequested() {
					state.RequestGraceful()
					continue
				}
				if writer != nil {
					fmt.Fprintln(writer, "Second interrupt received. Exiting immediately.")
				}
				state.Force()
				return
			}
		}
	}()
	return ctx, func() {
		stopOnce.Do(func() {
			signal.Stop(signals)
			close(stopCh)
		})
	}
}

func runInterruptFromContext(ctx context.Context) *runInterruptState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(runInterruptContextKey{}).(*runInterruptState)
	return state
}

func runGracefulContext(ctx context.Context) context.Context {
	if state := runInterruptFromContext(ctx); state != nil {
		return context.WithValue(state.gracefulCtx, runInterruptContextKey{}, state)
	}
	return ctx
}

func runGracefulShutdownRequested(ctx context.Context) bool {
	state := runInterruptFromContext(ctx)
	return state != nil && state.GracefulRequested()
}

func registerRunGracefulShutdownCallback(ctx context.Context, callback func()) {
	if callback == nil {
		return
	}
	state := runInterruptFromContext(ctx)
	if state == nil {
		return
	}
	state.OnGraceful(callback)
}

func (s *runInterruptState) RequestGraceful() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.gracefulRequested {
		s.mu.Unlock()
		return
	}
	s.gracefulRequested = true
	callbacks := append([]func(){}, s.gracefulCallbacks...)
	s.gracefulCallbackRun = true
	s.mu.Unlock()

	s.cancelGraceful()
	for _, callback := range callbacks {
		callback()
	}
}

func (s *runInterruptState) Force() {
	if s == nil {
		return
	}
	s.cancelForce()
}

func (s *runInterruptState) GracefulRequested() bool {
	if s == nil {
		return false
	}
	select {
	case <-s.gracefulCtx.Done():
		return true
	default:
		return false
	}
}

func (s *runInterruptState) OnGraceful(callback func()) {
	if s == nil || callback == nil {
		return
	}
	s.mu.Lock()
	runNow := s.gracefulRequested || s.gracefulCallbackRun
	if !runNow {
		s.gracefulCallbacks = append(s.gracefulCallbacks, callback)
	}
	s.mu.Unlock()
	if runNow {
		callback()
	}
}

func interruptedError(err error) error {
	if err == nil {
		return codedError{interruptExitCode, nil}
	}
	return codedError{interruptExitCode, err}
}
