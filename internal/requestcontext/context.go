// Package requestcontext contains context values shared by the HTTP and
// repository layers. Keeping correlation metadata here avoids making the
// repository depend on an HTTP implementation detail.
package requestcontext

import "context"

type correlationIDKey struct{}

// WithCorrelationID attaches a validated request/correlation identifier to a
// context. Callers should only pass identifiers that have already been
// bounded and validated at the transport boundary.
func WithCorrelationID(ctx context.Context, value string) context.Context {
	if value == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationIDKey{}, value)
}

// CorrelationID returns the request identifier associated with ctx.
func CorrelationID(ctx context.Context) string {
	value, _ := ctx.Value(correlationIDKey{}).(string)
	return value
}
