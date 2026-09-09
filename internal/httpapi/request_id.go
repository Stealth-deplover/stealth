package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nazxf/stealth-api/internal/requestcontext"
)

const requestIDHeader = "X-Request-ID"

// requestID accepts only a bounded, log-safe identifier from a trusted
// caller. Invalid or oversized values are replaced so an arbitrary header
// cannot inject newlines or unbounded data into structured logs.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}
		w.Header().Set(requestIDHeader, requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey, requestID)
		ctx = requestcontext.WithCorrelationID(ctx, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func newRequestID() string {
	if identifier, err := uuid.NewV7(); err == nil {
		return identifier.String()
	}
	return uuid.New().String() + "-" + time.Now().UTC().Format("20060102150405.000000000")
}

func requestIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(requestIDContextKey).(string)
	return value
}
