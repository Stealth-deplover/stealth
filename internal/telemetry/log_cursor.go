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

const logCursorVersion = 2

// LogCursor is the complete ordering key used by the live log tail. EventID is
// generated once by the pinned Collector before export and is persisted in the
// log record's attributes; it is not derived from visible log fields.
type LogCursor struct {
	Timestamp time.Time
	EventID   string
}

type logCursorPayload struct {
	Version   int       `json:"v"`
	Timestamp time.Time `json:"at"`
	EventID   string    `json:"event_id"`
}

// EncodeLogCursor produces an opaque, versioned value suitable for an SSE
// event id. It is deliberately not a SQL representation.
func EncodeLogCursor(cursor LogCursor) string {
	payload := logCursorPayload{
		Version:   logCursorVersion,
		Timestamp: cursor.Timestamp.UTC(),
		EventID:   cursor.EventID,
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
	if payload.Version != logCursorVersion || payload.Timestamp.IsZero() || strings.TrimSpace(payload.EventID) == "" || len(payload.EventID) > 256 {
		return LogCursor{}, ErrInvalidQuery
	}
	return LogCursor{
		Timestamp: payload.Timestamp.UTC(),
		EventID:   payload.EventID,
	}, nil
}

func (c LogCursor) After(other LogCursor) bool {
	if c.Timestamp.After(other.Timestamp) {
		return true
	}
	if !c.Timestamp.Equal(other.Timestamp) {
		return false
	}
	return c.EventID > other.EventID
}
