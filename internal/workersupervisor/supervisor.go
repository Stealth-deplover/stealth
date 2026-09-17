// Package workersupervisor owns the lifecycle policy shared by the worker
// process's independent queue loops.
package workersupervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Runner is the small lifecycle seam implemented by every queue worker.
type Runner interface {
	Run(context.Context) error
}

// RunnerFunc adapts a function to Runner for process-owned loops such as the
// private metrics server.
type RunnerFunc func(context.Context) error

func (f RunnerFunc) Run(ctx context.Context) error {
	return f(ctx)
}

// Registration gives a worker a stable name for actionable aggregate errors.
type Registration struct {
	Name   string
	Runner Runner
}

// Run starts all registered workers with one derived context. The first
// non-cancellation failure cancels the remaining workers, waits for every
// runner to exit, and returns the named failure. Parent cancellation is a
// normal shutdown and returns nil after all runners have stopped.
func Run(ctx context.Context, registrations ...Registration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRegistrations(registrations); err != nil {
		return err
	}
	if len(registrations) == 0 {
		return nil
	}

	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan workerResult, len(registrations))
	for _, registration := range registrations {
		go func(registration Registration) {
			results <- workerResult{name: registration.Name, err: registration.Runner.Run(workerContext)}
		}(registration)
	}

	var firstFailure error
	parentCancelled := false
	remaining := len(registrations)
	for remaining > 0 {
		var parentDone <-chan struct{}
		if !parentCancelled {
			parentDone = ctx.Done()
		}
		select {
		case <-parentDone:
			parentCancelled = true
			cancel()
		case result := <-results:
			remaining--
			if !parentCancelled {
				if firstFailure == nil && !isCancellation(result.err) {
					firstFailure = fmt.Errorf("%s: %w", result.name, result.err)
				}
				cancel()
			}
		}
	}
	if parentCancelled {
		return nil
	}
	return firstFailure
}

type workerResult struct {
	name string
	err  error
}

func validateRegistrations(registrations []Registration) error {
	seen := make(map[string]struct{}, len(registrations))
	for _, registration := range registrations {
		name := strings.TrimSpace(registration.Name)
		if name == "" || registration.Runner == nil {
			return errors.New("worker registration is incomplete")
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("worker registration %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func isCancellation(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
