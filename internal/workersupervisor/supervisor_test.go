package workersupervisor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type runnerFunc func(context.Context) error

func (f runnerFunc) Run(ctx context.Context) error {
	return f(ctx)
}

func TestRunCancelsSiblingsAndNamesFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 2)
	releaseFailure := make(chan struct{})
	siblingStopped := make(chan struct{})
	failure := errors.New("database connection lost")

	result := make(chan error, 1)
	go func() {
		result <- Run(ctx,
			Registration{Name: "failing", Runner: runnerFunc(func(context.Context) error {
				ready <- struct{}{}
				<-releaseFailure
				return failure
			})},
			Registration{Name: "sibling", Runner: runnerFunc(func(ctx context.Context) error {
				ready <- struct{}{}
				<-ctx.Done()
				close(siblingStopped)
				return ctx.Err()
			})},
		)
	}()

	<-ready
	<-ready
	close(releaseFailure)
	err := <-result
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "failing") {
		t.Fatalf("Run() error = %v, want named failure", err)
	}
	select {
	case <-siblingStopped:
	default:
		t.Fatal("sibling was not cancelled before Run returned")
	}
}

func TestRunTreatsParentCancellationAsNormalShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		if err := Run(ctx, Registration{Name: "worker", Runner: runnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})}); err != nil {
			t.Errorf("Run() error = %v, want nil", err)
		}
		close(done)
	}()
	cancel()
	<-done
}

func TestRunRejectsIncompleteOrDuplicateRegistrations(t *testing.T) {
	runner := runnerFunc(func(context.Context) error { return nil })
	for name, registrations := range map[string][]Registration{
		"missing name":   {{Runner: runner}},
		"missing runner": {{Name: "worker"}},
		"duplicate name": {{Name: "worker", Runner: runner}, {Name: "worker", Runner: runner}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Run(context.Background(), registrations...); err == nil {
				t.Fatal("Run() accepted invalid registrations")
			}
		})
	}
}
