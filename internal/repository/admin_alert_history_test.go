package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAdminAlertEventCursorRoundTripPreservesFilters(t *testing.T) {
	ruleID := uuid.MustParse("00000000-0000-7000-8000-000000000101")
	from := time.Date(2026, 9, 20, 10, 0, 0, 123456789, time.FixedZone("test", 2*60*60))
	to := from.Add(30 * time.Minute)
	want := AdminAlertEventCursor{
		OccurredAt: from.Add(5 * time.Minute),
		ID:         uuid.MustParse("00000000-0000-7000-8000-000000000102"),
		RuleID:     &ruleID,
		From:       &from,
		To:         &to,
	}

	encoded := EncodeAdminAlertEventCursor(want)
	if encoded == "" || encoded == "{" {
		t.Fatalf("EncodeAdminAlertEventCursor() = %q, want opaque value", encoded)
	}
	got, err := DecodeAdminAlertEventCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeAdminAlertEventCursor() error = %v", err)
	}
	if !got.OccurredAt.Equal(want.OccurredAt) || got.ID != want.ID || !sameOptionalUUID(got.RuleID, want.RuleID) || !sameOptionalTime(got.From, want.From) || !sameOptionalTime(got.To, want.To) {
		t.Fatalf("decoded cursor = %#v, want %#v", got, want)
	}
}

func TestAdminAlertEventCursorRejectsMalformedOrUnknownVersion(t *testing.T) {
	for _, value := range []string{"not-a-cursor", "eyJ2Ijo5fQ"} {
		t.Run(value, func(t *testing.T) {
			if _, err := DecodeAdminAlertEventCursor(value); !errors.Is(err, ErrInvalidAdminAlertHistory) {
				t.Fatalf("DecodeAdminAlertEventCursor(%q) error = %v, want invalid history query", value, err)
			}
		})
	}
}

func TestAdminAlertEventQueryRejectsCursorForDifferentFilters(t *testing.T) {
	ruleA := uuid.MustParse("00000000-0000-7000-8000-000000000103")
	ruleB := uuid.MustParse("00000000-0000-7000-8000-000000000104")
	occurredAt := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	cursor := AdminAlertEventCursor{OccurredAt: occurredAt, ID: uuid.MustParse("00000000-0000-7000-8000-000000000105"), RuleID: &ruleA}

	_, err := normalizeAdminAlertEventQuery(AdminAlertEventQuery{RuleID: &ruleB, Cursor: &cursor, Limit: 10})
	if !errors.Is(err, ErrInvalidAdminAlertHistory) {
		t.Fatalf("normalizeAdminAlertEventQuery() error = %v, want filter mismatch", err)
	}
}
