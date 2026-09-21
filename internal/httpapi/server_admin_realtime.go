package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

const (
	adminRealtimePollInterval = time.Second
	adminRealtimeHeartbeat    = 15 * time.Second
	adminRealtimeBatchSize    = 100
)

// adminRealtime streams durable, instance-scoped invalidation events. It uses
// the same cursor/SSE contract as project realtime, but polls the bounded
// PostgreSQL outbox because admin events have no project scope. The browser
// refetches canonical state after each notification; no resource snapshot is
// streamed.
func (s *Server) adminRealtime(w http.ResponseWriter, r *http.Request) {
	after, err := realtimeCursor(r)
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
	for {
		items, next, listErr := s.repo.ListAdminRealtimeEvents(r.Context(), after, adminRealtimeBatchSize)
		if listErr != nil {
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", strconv.Quote("admin realtime stream unavailable"))
			flusher.Flush()
			return
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
		}
	}
}

func writeAdminRealtimeEvent(w http.ResponseWriter, flusher http.Flusher, item repository.AdminRealtimeEvent) error {
	if item.ID == uuid.Nil || len(item.Payload) == 0 || !json.Valid(item.Payload) {
		return fmt.Errorf("invalid admin realtime event")
	}
	if _, err := fmt.Fprintf(w, "id: %s\nevent: admin\ndata: %s\n\n", item.ID, item.Payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
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
