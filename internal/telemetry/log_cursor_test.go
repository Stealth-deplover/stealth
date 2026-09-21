package telemetry

import (
	"errors"
	"testing"
	"time"
)

func TestLogCursorRoundTrip(t *testing.T) {
	want := LogCursor{
		Timestamp: time.Date(2026, 9, 21, 12, 34, 56, 123456789, time.FixedZone("test", 3600)),
		TraceID:   "trace-id",
		SpanID:    "span-id",
		Tie:       42,
	}
	encoded := EncodeLogCursor(want)
	if encoded == "" || encoded == "trace-id" || encoded == "span-id" {
		t.Fatalf("cursor was not opaque: %q", encoded)
	}
	got, err := DecodeLogCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Timestamp.Equal(want.Timestamp.UTC()) || got.TraceID != want.TraceID || got.SpanID != want.SpanID || got.Tie != want.Tie {
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
}
