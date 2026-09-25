package httpapi

import (
	"errors"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/telemetry"
)

func TestResolveAppRuntimeLogRange(t *testing.T) {
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	maxRange := 4 * time.Hour
	oldCursor := telemetry.LogCursor{
		Timestamp: now.Add(-90 * time.Minute),
		EventID:   "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4d3c",
	}
	explicitEarlierFrom := now.Add(-2 * time.Hour)
	explicitLaterFrom := oldCursor.Timestamp.Add(time.Minute)
	explicitTo := now.Add(-time.Minute)
	futureCursor := telemetry.LogCursor{
		Timestamp: now.Add(time.Minute),
		EventID:   oldCursor.EventID,
	}
	expiredCursor := telemetry.LogCursor{
		Timestamp: now.Add(-3 * time.Hour),
		EventID:   oldCursor.EventID,
	}
	shortRange := time.Hour
	minimumRange := 15 * time.Minute
	futureTo := now.Add(6 * time.Minute)
	equalToCursor := oldCursor.Timestamp

	tests := []struct {
		name     string
		from     *time.Time
		to       *time.Time
		cursor   *telemetry.LogCursor
		maxRange time.Duration
		wantFrom time.Time
		wantTo   time.Time
		wantErr  error
	}{
		{
			name:     "initial request defaults to recent hour",
			maxRange: maxRange,
			wantFrom: now.Add(-time.Hour),
			wantTo:   now,
		},
		{
			name:     "cursor without explicit from resumes at cursor timestamp",
			cursor:   &oldCursor,
			maxRange: maxRange,
			wantFrom: oldCursor.Timestamp,
			wantTo:   now,
		},
		{
			name:     "initial lookback stays within configured maximum",
			maxRange: minimumRange,
			wantFrom: now.Add(-minimumRange),
			wantTo:   now,
		},
		{
			name:     "earlier explicit from remains bounded and includes cursor",
			from:     &explicitEarlierFrom,
			to:       &explicitTo,
			cursor:   &oldCursor,
			maxRange: maxRange,
			wantFrom: explicitEarlierFrom,
			wantTo:   explicitTo,
		},
		{
			name:     "explicit from after cursor is rejected",
			from:     &explicitLaterFrom,
			cursor:   &oldCursor,
			maxRange: maxRange,
			wantErr:  errAppRuntimeLogFromAfterCursor,
		},
		{
			name:     "cursor beyond max query range is rejected",
			cursor:   &expiredCursor,
			maxRange: shortRange,
			wantErr:  errAppRuntimeLogCursorOutsideRange,
		},
		{
			name:     "future cursor is rejected",
			cursor:   &futureCursor,
			maxRange: maxRange,
			wantErr:  errInvalidAppRuntimeLogCursor,
		},
		{
			name:     "to must be after cursor",
			to:       &equalToCursor,
			cursor:   &oldCursor,
			maxRange: maxRange,
			wantErr:  errAppRuntimeLogToBeforeCursor,
		},
		{
			name:     "far future to is rejected",
			to:       &futureTo,
			maxRange: maxRange,
			wantErr:  errAppRuntimeLogToTooFarFuture,
		},
		{
			name:    "missing configured max range is rejected",
			wantErr: errInvalidAppRuntimeLogRange,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveAppRuntimeLogRange(now, test.from, test.to, test.cursor, test.maxRange)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveAppRuntimeLogRange() error = %v", err)
			}
			if !got.From.Equal(test.wantFrom) || !got.To.Equal(test.wantTo) {
				t.Fatalf("range = [%s, %s], want [%s, %s]", got.From, got.To, test.wantFrom, test.wantTo)
			}
		})
	}
}
