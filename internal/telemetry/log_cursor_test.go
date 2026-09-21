package telemetry

import (
	"errors"
	"testing"
	"time"
)

func TestLogCursorRoundTrip(t *testing.T) {
	want := LogCursor{
		Timestamp: time.Date(2026, 9, 21, 12, 34, 56, 123456789, time.FixedZone("test", 3600)),
		EventID:   "0198f3d8-7c2f-7b2e-8a9e-8c7d6f5e4d3c",
	}
	encoded := EncodeLogCursor(want)
	if encoded == "" || encoded == "trace-id" || encoded == "span-id" {
		t.Fatalf("cursor was not opaque: %q", encoded)
	}
	got, err := DecodeLogCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Timestamp.Equal(want.Timestamp.UTC()) || got.EventID != want.EventID {
		t.Fatalf("cursor = %+v, want %+v", got, want)
	}
}

func TestLogCursorRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"not-a-cursor", "", "e30"} {
		if value == "" {
			continue
		}
		if _, err := DecodeLogCursor(value); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("DecodeLogCursor(%q) error = %v, want ErrInvalidQuery", value, err)
		}
	}
	if _, err := DecodeLogCursor(EncodeLogCursor(LogCursor{Timestamp: time.Now().UTC(), EventID: ""})); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("DecodeLogCursor accepted a cursor without a persisted event ID: %v", err)
	}
}
