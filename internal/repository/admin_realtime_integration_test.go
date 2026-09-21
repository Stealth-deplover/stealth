package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type adminRealtimeIntegrationDB struct {
	pool *pgxpool.Pool
	ctx  context.Context
}

func newAdminRealtimeIntegrationDB(t *testing.T) adminRealtimeIntegrationDB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return adminRealtimeIntegrationDB{pool: pool, ctx: ctx}
}

func TestAdminRealtimeSequenceCommitOrderingIntegration(t *testing.T) {
	database := newAdminRealtimeIntegrationDB(t)
	ctx := database.ctx
	targetAID := uuid.Must(uuid.NewV7())
	targetBID := uuid.Must(uuid.NewV7())
	t.Cleanup(func() { cleanupAdminRealtimeTestEvents(t, database.pool, targetAID, targetBID) })

	txA, err := database.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer txA.Rollback(ctx)
	if err := enqueueAdminRealtimeEventTx(ctx, txA, "admin.realtime.test.a", "admin_realtime_test", targetAID, map[string]any{"marker": "commit-order-a"}); err != nil {
		t.Fatal(err)
	}
	eventAID, sequenceA, err := adminRealtimeEventRow(ctx, txA, targetAID)
	if err != nil {
		t.Fatal(err)
	}

	type producerResult struct {
		eventID  uuid.UUID
		sequence int64
		err      error
	}
	bStarted := make(chan int32, 1)
	bDone := make(chan producerResult, 1)
	go func() {
		txB, beginErr := database.pool.Begin(ctx)
		if beginErr != nil {
			bDone <- producerResult{err: beginErr}
			return
		}
		defer txB.Rollback(ctx)
		var pid int32
		if scanErr := txB.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); scanErr != nil {
			bDone <- producerResult{err: scanErr}
			return
		}
		bStarted <- pid
		if enqueueErr := enqueueAdminRealtimeEventTx(ctx, txB, "admin.realtime.test.b", "admin_realtime_test", targetBID, map[string]any{"marker": "commit-order-b"}); enqueueErr != nil {
			bDone <- producerResult{err: enqueueErr}
			return
		}
		eventBID, sequenceB, queryErr := adminRealtimeEventRow(ctx, txB, targetBID)
		if queryErr != nil {
			bDone <- producerResult{err: queryErr}
			return
		}
		if commitErr := txB.Commit(ctx); commitErr != nil {
			bDone <- producerResult{err: commitErr}
			return
		}
		bDone <- producerResult{eventID: eventBID, sequence: sequenceB}
	}()

	var pid int32
	select {
	case pid = <-bStarted:
	case result := <-bDone:
		t.Fatalf("producer B failed before attempting enqueue: %v", result.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := waitForAdminRealtimeOrderingWait(ctx, database.pool, pid); err != nil {
		t.Fatal(err)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var result producerResult
	select {
	case result = <-bDone:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.sequence <= sequenceA {
		t.Fatalf("commit-order sequences A=%d B=%d; want A < B", sequenceA, result.sequence)
	}

	beforeA := sequenceA - 1
	var firstPageAfter *int64
	if beforeA > 0 {
		firstPageAfter = &beforeA
	}
	items, next, err := databaseRepository(database.pool).ListAdminRealtimeEvents(ctx, firstPageAfter, 100)
	if err != nil {
		t.Fatal(err)
	}
	ordered := filterAdminRealtimeTestEvents(items, eventAID, result.eventID)
	if len(ordered) != 2 || ordered[0].ID != eventAID || ordered[1].ID != result.eventID {
		t.Fatalf("ordered events after pre-A cursor = %#v, next=%v; want A then B", ordered, next)
	}
	afterA := sequenceA
	items, _, err = databaseRepository(database.pool).ListAdminRealtimeEvents(ctx, &afterA, 100)
	if err != nil {
		t.Fatal(err)
	}
	ordered = filterAdminRealtimeTestEvents(items, eventAID, result.eventID)
	if len(ordered) != 1 || ordered[0].ID != result.eventID {
		t.Fatalf("events after A cursor = %#v; want B only", ordered)
	}
}

func TestAdminRealtimeSequenceRollbackIntegration(t *testing.T) {
	database := newAdminRealtimeIntegrationDB(t)
	ctx := database.ctx
	targetAID := uuid.Must(uuid.NewV7())
	targetBID := uuid.Must(uuid.NewV7())
	t.Cleanup(func() { cleanupAdminRealtimeTestEvents(t, database.pool, targetAID, targetBID) })

	txA, err := database.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := enqueueAdminRealtimeEventTx(ctx, txA, "admin.realtime.test.rollback", "admin_realtime_test", targetAID, map[string]any{"marker": "rollback"}); err != nil {
		t.Fatal(err)
	}
	_, sequenceA, err := adminRealtimeEventRow(ctx, txA, targetAID)
	if err != nil {
		t.Fatal(err)
	}
	if err := txA.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	txB, err := database.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := enqueueAdminRealtimeEventTx(ctx, txB, "admin.realtime.test.committed", "admin_realtime_test", targetBID, map[string]any{"marker": "committed"}); err != nil {
		t.Fatal(err)
	}
	eventBID, sequenceB, err := adminRealtimeEventRow(ctx, txB, targetBID)
	if err != nil {
		t.Fatal(err)
	}
	if err := txB.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if sequenceB <= sequenceA {
		t.Fatalf("rollback sequences A=%d B=%d; want committed B after rolled-back A", sequenceA, sequenceB)
	}

	var phantom int
	if err := database.pool.QueryRow(ctx, `SELECT count(*) FROM admin_realtime_events WHERE target_id=$1`, targetAID).Scan(&phantom); err != nil {
		t.Fatal(err)
	}
	if phantom != 0 {
		t.Fatalf("rolled-back event row count = %d, want 0", phantom)
	}
	afterA := sequenceA
	items, _, err := databaseRepository(database.pool).ListAdminRealtimeEvents(ctx, &afterA, 100)
	if err != nil {
		t.Fatal(err)
	}
	ordered := filterAdminRealtimeTestEvents(items, eventBID)
	if len(ordered) != 1 || ordered[0].ID != eventBID {
		t.Fatalf("events after rolled-back sequence = %#v; want committed B only", ordered)
	}
}

func adminRealtimeEventRow(ctx context.Context, tx pgx.Tx, targetID uuid.UUID) (uuid.UUID, int64, error) {
	var id uuid.UUID
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT id,sequence FROM admin_realtime_events WHERE target_id=$1`, targetID).Scan(&id, &sequence); err != nil {
		return uuid.Nil, 0, err
	}
	return id, sequence, nil
}

func waitForAdminRealtimeOrderingWait(ctx context.Context, pool *pgxpool.Pool, pid int32) error {
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(deadline, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_locks
				WHERE locktype='advisory' AND pid=$1 AND NOT granted
			)`, pid).Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("producer pid %d did not wait for the realtime ordering lock: %w", pid, deadline.Err())
		case <-ticker.C:
		}
	}
}

func filterAdminRealtimeTestEvents(items []AdminRealtimeEvent, ids ...uuid.UUID) []AdminRealtimeEvent {
	wanted := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	filtered := make([]AdminRealtimeEvent, 0, len(ids))
	for _, item := range items {
		if _, ok := wanted[item.ID]; ok {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func cleanupAdminRealtimeTestEvents(t *testing.T, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, id := range ids {
		if _, err := pool.Exec(ctx, `DELETE FROM admin_realtime_events WHERE target_id=$1`, id); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("cleanup event %s: %v", id, err)
		}
	}
}

func databaseRepository(pool *pgxpool.Pool) *Repository {
	return New(pool)
}
