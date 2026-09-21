package telemetry

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const logCursorVersion = 1

// LogCursor is the complete ordering key used by the live log tail. The
// exporter schema does not provide a synthetic row id, so the final key is a
// deterministic hash of the visible log fields in addition to the OTel trace
// identity fields.
type LogCursor struct {
	Timestamp time.Time
	TraceID   string
	SpanID    string
	Tie       uint64
}

type logCursorPayload struct {
	Version   int       `json:"v"`
	Timestamp time.Time `json:"at"`
	TraceID   string    `json:"trace_id"`
	SpanID    string    `json:"span_id"`
	Tie       uint64    `json:"tie"`
}

// EncodeLogCursor produces an opaque, versioned value suitable for an SSE
// event id. It is deliberately not a SQL representation.
func EncodeLogCursor(cursor LogCursor) string {
	payload := logCursorPayload{
		Version:   logCursorVersion,
		Timestamp: cursor.Timestamp.UTC(),
		TraceID:   cursor.TraceID,
		SpanID:    cursor.SpanID,
		Tie:       cursor.Tie,
	}
	data, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(data)
}

// DecodeLogCursor validates the opaque SSE resume value. Invalid values are
// treated as a fresh bounded tail by the HTTP layer rather than being exposed
// as backend or implementation errors.
func DecodeLogCursor(value string) (LogCursor, error) {
	if strings.TrimSpace(value) == "" {
		return LogCursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return LogCursor{}, ErrInvalidQuery
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload logCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return LogCursor{}, ErrInvalidQuery
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return LogCursor{}, ErrInvalidQuery
	}
	if payload.Version != logCursorVersion || payload.Timestamp.IsZero() || len(payload.TraceID) > 256 || len(payload.SpanID) > 256 {
		return LogCursor{}, ErrInvalidQuery
	}
	return LogCursor{
		Timestamp: payload.Timestamp.UTC(),
		TraceID:   payload.TraceID,
		SpanID:    payload.SpanID,
		Tie:       payload.Tie,
	}, nil
}

func (c LogCursor) After(other LogCursor) bool {
	if c.Timestamp.After(other.Timestamp) {
		return true
	}
	if !c.Timestamp.Equal(other.Timestamp) {
		return false
	}
	if c.TraceID != other.TraceID {
		return c.TraceID > other.TraceID
	}
	if c.SpanID != other.SpanID {
		return c.SpanID > other.SpanID
	}
	return c.Tie > other.Tie
}
