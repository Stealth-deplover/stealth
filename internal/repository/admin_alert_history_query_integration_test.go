package repository

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func insertAdminAlertHistoryEvent(t *testing.T, fixture adminAlertHistoryFixture, occurredAt time.Time, state string) uuid.UUID {
	t.Helper()
	eventID := uuid.Must(uuid.NewV7())
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO admin_alert_events
			(id,rule_id,state,value,message,occurred_at,rule_id_snapshot,rule_name_snapshot,rule_kind_snapshot,severity_snapshot,condition_snapshot)
		VALUES ($1,$2,$3,91,$4,$5,$2,'Delete history regression','metric_threshold','critical',
			'{"operator":"gte","threshold":90,"metric":"system.cpu.utilization"}'::jsonb)`,
		eventID, fixture.ruleID, state, "history event "+eventID.String(), occurredAt); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func insertAdminAlertHistoryRule(t *testing.T, fixture adminAlertHistoryFixture) uuid.UUID {
	t.Helper()
	ruleID := uuid.Must(uuid.NewV7())
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO admin_alert_rules (id,name,kind,condition,severity,created_by_account_id)
		VALUES ($1,'Second history rule','metric_threshold','{"operator":"gte","threshold":80}'::jsonb,'warning',$2)`, ruleID, fixture.accountID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = fixture.pool.Exec(context.Background(), `DELETE FROM admin_alert_events WHERE rule_id=$1 OR rule_id_snapshot=$1`, ruleID)
		_, _ = fixture.pool.Exec(context.Background(), `DELETE FROM admin_alert_rules WHERE id=$1`, ruleID)
	})
	return ruleID
}

func collectAdminAlertEventPages(t *testing.T, query AdminAlertEventQuery, fetch func(AdminAlertEventQuery) (AdminAlertEventPage, error)) []string {
	t.Helper()
	ids := make([]string, 0)
	for {
		page, err := fetch(query)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		if page.NextCursor == "" {
			return ids
		}
		cursor, err := DecodeAdminAlertEventCursor(page.NextCursor)
		if err != nil {
			t.Fatalf("DecodeAdminAlertEventCursor(next) error = %v", err)
		}
		query.Cursor = &cursor
	}
}

func TestQueryAdminAlertEventsGlobalCursorPaginationIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 1)
	occurredAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	ids := make([]uuid.UUID, 0, 7)
	for range 7 {
		ids = append(ids, insertAdminAlertHistoryEvent(t, fixture, occurredAt, "firing"))
	}
	want := append([]uuid.UUID(nil), ids...)
	sort.Slice(want, func(i, j int) bool { return want[i].String() > want[j].String() })
	from := occurredAt.Add(-time.Minute)
	to := occurredAt.Add(time.Minute)
	query := AdminAlertEventQuery{From: &from, To: &to, Limit: 2}
	got := collectAdminAlertEventPages(t, query, func(query AdminAlertEventQuery) (AdminAlertEventPage, error) {
		return fixture.repo.QueryAdminAlertEvents(context.Background(), query)
	})
	if len(got) != len(want) {
		t.Fatalf("global history returned %d events, want %d", len(got), len(want))
	}
	for index, id := range want {
		if got[index] != id.String() {
			t.Fatalf("global history page order[%d] = %s, want %s", index, got[index], id)
		}
	}
}

func TestQueryAdminAlertEventsPerRulePaginationSurvivesDeletionIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 1)
	ruleB := insertAdminAlertHistoryRule(t, fixture)
	occurredAt := time.Date(2026, 9, 20, 12, 5, 0, 0, time.UTC)
	for range 5 {
		insertAdminAlertHistoryEvent(t, fixture, occurredAt, "firing")
	}
	otherEventID := uuid.Must(uuid.NewV7())
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO admin_alert_events
			(id,rule_id,state,value,message,occurred_at,rule_id_snapshot,rule_name_snapshot,rule_kind_snapshot,severity_snapshot,condition_snapshot)
		VALUES ($1,$2,'firing',80,'other rule event',$3,$2,'Second history rule','metric_threshold','warning','{"operator":"gte","threshold":80}'::jsonb)`, otherEventID, ruleB, occurredAt); err != nil {
		t.Fatal(err)
	}
	fixture.deleteRule(t)

	ruleID := fixture.ruleID
	query := AdminAlertEventQuery{RuleID: &ruleID, Limit: 2}
	seen := make(map[string]bool)
	for {
		page, err := fixture.repo.QueryAdminAlertEvents(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if item.ID == otherEventID.String() || seen[item.ID] || item.RuleID != fixture.ruleID.String() || item.SourceRuleExists {
				t.Fatalf("per-rule deleted history item = %#v, want deleted rule snapshot", item)
			}
			seen[item.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor, err := DecodeAdminAlertEventCursor(page.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		query.Cursor = &cursor
	}
	var count int
	if err := fixture.pool.QueryRow(context.Background(), `SELECT count(*) FROM admin_alert_events WHERE rule_id_snapshot=$1`, fixture.ruleID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 || len(seen) != 5 {
		t.Fatalf("deleted rule history count = %d/%d, want 5 unique events", count, len(seen))
	}
}

func TestQueryAdminAlertEventsTimeRangeIntegration(t *testing.T) {
	fixture := newAdminAlertHistoryFixture(t, 1)
	base := time.Date(2026, 9, 20, 13, 0, 0, 0, time.UTC)
	insertAdminAlertHistoryEvent(t, fixture, base.Add(-time.Minute), "firing")
	insideID := insertAdminAlertHistoryEvent(t, fixture, base.Add(5*time.Minute), "resolved")
	upperBoundaryID := insertAdminAlertHistoryEvent(t, fixture, base.Add(10*time.Minute), "firing")
	insertAdminAlertHistoryEvent(t, fixture, base.Add(time.Hour), "firing")
	from := base.Add(5 * time.Minute)
	to := base.Add(10 * time.Minute)
	query := AdminAlertEventQuery{From: &from, To: &to, Limit: 10}
	global, err := fixture.repo.QueryAdminAlertEvents(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(global.Items) != 2 {
		t.Fatalf("global time range = %#v, want the inclusive boundary events", global.Items)
	}
	if (global.Items[0].ID != insideID.String() && global.Items[0].ID != upperBoundaryID.String()) || (global.Items[1].ID != insideID.String() && global.Items[1].ID != upperBoundaryID.String()) || global.Items[0].ID == global.Items[1].ID {
		t.Fatalf("global time range IDs = %#v, want %s and %s", global.Items, insideID, upperBoundaryID)
	}
	ruleID := fixture.ruleID
	query.RuleID = &ruleID
	perRule, err := fixture.repo.QueryAdminAlertEvents(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if len(perRule.Items) != 2 {
		t.Fatalf("per-rule time range = %#v, want the inclusive boundary events", perRule.Items)
	}
}
