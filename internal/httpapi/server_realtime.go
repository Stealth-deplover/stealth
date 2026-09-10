package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/domain"
	"github.com/nazxf/stealth-api/internal/repository"
	"github.com/redis/go-redis/v9"
)

const (
	realtimePollInterval = 5 * time.Second
	realtimeHeartbeat    = 15 * time.Second
	realtimeBatchSize    = 100
)

// realtime streams the project event outbox as Server-Sent Events. The
// transport deliberately reuses the permission-aware data actor middleware:
// Console sessions and realtime.read API keys can inspect the complete stream,
// while application sessions receive only row events they could read through
// the Database API. Anonymous callers are rejected by the repository.
func (s *Server) realtime(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathUUID(w, r, "projectID")
	if !ok {
		return
	}
	after, err := realtimeCursor(r)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
		return
	}
	filter, err := realtimeEventFilter(r.URL.Query().Get("events"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_error", err.Error())
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

	// Authenticate and establish the project-scoped boundary before committing
	// to a streaming response so errors remain a normal JSON envelope. With no
	// cursor this starts at the current retained tail. A later PostgreSQL poll
	// reconciles events committed between this lookup and Redis subscription,
	// so the tail lookup cannot create a lost-event window.
	actor := databaseActorFrom(r)
	after, err = s.repo.ResolveRealtimeStartCursor(r.Context(), projectID, actor, after)
	if realtimeResourceError(w, err) {
		return
	}
	if s.metrics != nil {
		s.metrics.RealtimeConnections.Inc()
		s.metrics.RealtimeActiveConnections.Inc()
		defer s.metrics.RealtimeActiveConnections.Dec()
	}
	var realtimeSubscription *redis.PubSub
	if s.realtimeBroker != nil {
		subscription, subscribeErr := s.realtimeBroker.Subscribe(r.Context(), projectID.String())
		if subscribeErr != nil {
			// Redis is only the low-latency fanout. The database outbox remains
			// available as a bounded polling fallback when Redis is degraded.
			s.logger.Warn("realtime fanout unavailable; using database polling", "error", subscribeErr, "project_id", projectID, "request_id", requestIDFrom(r.Context()))
		} else {
			realtimeSubscription = subscription
			defer realtimeSubscription.Close()
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

	poll := time.NewTicker(realtimePollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(realtimeHeartbeat)
	defer heartbeat.Stop()
	var realtimeMessages <-chan *redis.Message
	if realtimeSubscription != nil {
		realtimeMessages = realtimeSubscription.Channel(redis.WithChannelSize(32))
	}
	for {
		items, next, listErr := s.repo.ListRealtimeEvents(r.Context(), projectID, actor, after, realtimeBatchSize)
		if listErr != nil {
			// Once headers have been sent an SSE error cannot be represented as
			// JSON. Send a terminal event and close; clients can reconnect using
			// the last event id they received.
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", strconv.Quote("realtime stream unavailable"))
			flusher.Flush()
			return
		}
		if next != nil {
			after = next
		}
		for _, item := range items {
			if !filter.matches(item.EventName) {
				continue
			}
			if err := writeRealtimeEvent(w, flusher, item); err != nil {
				if s.metrics != nil {
					s.metrics.RealtimeSlowDisconnects.Inc()
				}
				return
			}
			if s.metrics != nil {
				s.metrics.RealtimeEventsDelivered.Inc()
			}
		}
		if len(items) > 0 {
			// Drain any backlog without waiting for the next ticker tick.
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-realtimeMessages:
			if !ok {
				realtimeMessages = nil
			}
			// Redis messages are notifications only. The next loop reads the
			// authorized canonical state from PostgreSQL using the cursor.
			continue
		case <-poll.C:
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeRealtimeEvent(w http.ResponseWriter, flusher http.Flusher, item domain.RealtimeEvent) error {
	if len(item.Payload) == 0 || strings.ContainsAny(item.EventName, "\r\n") {
		return errors.New("invalid realtime event")
	}
	if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\n", item.ID, item.EventName); err != nil {
		return err
	}
	// JSON generated by the repository is compact, but split defensively so a
	// future payload formatter cannot inject a new SSE field through a raw
	// newline.
	for _, line := range strings.Split(string(item.Payload), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(w, "\n"); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func realtimeCursor(r *http.Request) (*uuid.UUID, error) {
	// Browsers send Last-Event-ID automatically on native EventSource
	// reconnects. Treat it as authoritative; the explicit query cursor is the
	// fallback for clients that cannot set the header.
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("cursor"))
	}
	if value == "" {
		return nil, nil
	}
	if len(value) > 64 {
		return nil, errors.New("cursor is too long")
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, errors.New("cursor must be a UUID")
	}
	return &id, nil
}

type realtimeFilter struct {
	all    bool
	events map[string]struct{}
}

func (f realtimeFilter) matches(event string) bool {
	if f.all {
		return true
	}
	_, ok := f.events[event]
	return ok
}

func realtimeEventFilter(raw string) (realtimeFilter, error) {
	if strings.TrimSpace(raw) == "" {
		return realtimeFilter{all: true}, nil
	}
	parts := strings.Split(raw, ",")
	normalized, err := repository.NormalizeWebhookEvents(parts)
	if err != nil {
		return realtimeFilter{}, errors.New("events must be comma-separated event names")
	}
	filter := realtimeFilter{events: make(map[string]struct{}, len(normalized))}
	for _, event := range normalized {
		if event == "*" {
			filter.all = true
			continue
		}
		filter.events[event] = struct{}{}
	}
	return filter, nil
}

func realtimeResourceError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, repository.ErrRealtimeForbidden), errors.Is(err, repository.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "you do not have permission to read project realtime events")
	case errors.Is(err, repository.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "project was not found")
	case errors.Is(err, repository.ErrInvalidRealtime):
		writeError(w, http.StatusUnprocessableEntity, "validation_error", "realtime request is invalid")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to read realtime events")
	}
	return true
}
