package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

const (
	adminRealtimePollInterval        = time.Second
	adminRealtimeHeartbeat           = 15 * time.Second
	adminRealtimeAuthRecheckInterval = 15 * time.Second
	adminRealtimeBatchSize           = 100
	adminRealtimeCursorVersion       = 2
)

var errInvalidAdminRealtimeCursor = errors.New("admin realtime cursor is invalid")

type adminRealtimeCursorPayload struct {
	Version  int   `json:"v"`
	Sequence int64 `json:"sequence"`
}

// adminRealtime streams durable, instance-scoped invalidation events. It uses
// the same cursor/SSE contract as project realtime, but polls the bounded
// PostgreSQL outbox because admin events have no project scope. The browser
// refetches canonical state after each notification; no resource snapshot is
// streamed.
func (s *Server) adminRealtime(w http.ResponseWriter, r *http.Request) {
	after, err := decodeAdminRealtimeCursor(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	after, err = s.repo.ResolveAdminRealtimeStartCursor(r.Context(), after)
	if err != nil {
		adminRealtimeError(w, err)
		return
	}
	if s.realtimeSlots != nil {
		select {
		case s.realtimeSlots <- struct{}{}:
			defer func() { <-s.realtimeSlots }()
		default:
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many realtime streams are active")
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "streaming is not supported by this server")
		return
	}
	authorized := func() bool {
		allowed, checkErr := s.repo.IsInstanceAdminSession(r.Context(), mustUUID(accountFrom(r).ID), sessionFrom(r))
		return checkErr == nil && allowed
	}
	if !authorized() {
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	poll := time.NewTicker(adminRealtimePollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(adminRealtimeHeartbeat)
	defer heartbeat.Stop()
	authInterval := s.adminRealtimeAuthRecheckInterval
	if authInterval <= 0 {
		authInterval = adminRealtimeAuthRecheckInterval
	}
	authCheck := time.NewTicker(authInterval)
	defer authCheck.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-authCheck.C:
			if !authorized() {
				return
			}
		default:
		}
		items, next, listErr := s.repo.ListAdminRealtimeEvents(r.Context(), after, adminRealtimeBatchSize)
		if listErr != nil {
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", strconv.Quote("admin realtime stream unavailable"))
			flusher.Flush()
			return
		}
		select {
		case <-authCheck.C:
			if !authorized() {
				return
			}
		default:
		}
		if next != nil {
			after = next
		}
		for _, item := range items {
			if err := writeAdminRealtimeEvent(w, flusher, item); err != nil {
				return
			}
		}
		if len(items) > 0 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-authCheck.C:
			if !authorized() {
				return
			}
		}
	}
}

func writeAdminRealtimeEvent(w http.ResponseWriter, flusher http.Flusher, item repository.AdminRealtimeEvent) error {
	if item.ID == uuid.Nil || item.Sequence <= 0 || len(item.Payload) == 0 || !json.Valid(item.Payload) {
		return fmt.Errorf("invalid admin realtime event")
	}
	if _, err := fmt.Fprintf(w, "id: %s\nevent: admin\ndata: %s\n\n", encodeAdminRealtimeCursor(item.Sequence), item.Payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func encodeAdminRealtimeCursor(sequence int64) string {
	payload, _ := json.Marshal(adminRealtimeCursorPayload{Version: adminRealtimeCursorVersion, Sequence: sequence})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeAdminRealtimeCursor(r *http.Request) (*int64, error) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("cursor"))
	}
	if value == "" {
		return nil, nil
	}
	if len(value) > 256 {
		return nil, fmt.Errorf("cursor is too long")
	}
	// PR #89 has not shipped the UUID cursor, but browsers may still send one
	// from a pre-sequence development build. Treat it as stale and start at the
	// current retained tail instead of interpreting UUID bytes as a sequence.
	if _, err := uuid.Parse(value); err == nil {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errInvalidAdminRealtimeCursor
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var payload adminRealtimeCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, errInvalidAdminRealtimeCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errInvalidAdminRealtimeCursor
	}
	if payload.Version != adminRealtimeCursorVersion || payload.Sequence <= 0 {
		return nil, errInvalidAdminRealtimeCursor
	}
	sequence := payload.Sequence
	return &sequence, nil
}

func adminRealtimeError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if err == repository.ErrInvalidAdminRealtime {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "admin realtime request is invalid")
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "unable to read admin realtime events")
}
