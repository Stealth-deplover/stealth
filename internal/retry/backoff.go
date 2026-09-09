// Package retry contains small, bounded retry policies shared by durable
// delivery workers. It deliberately does not decide whether an error is
// retryable or how many attempts a job gets; callers own those semantics.
package retry

import (
	"math/rand"
	"time"
)

// Exponential returns a bounded exponential delay with +/-20% jitter. The
// first attempt uses base, subsequent attempts double up to maximum, and no
// result can exceed maximum. A bounded jitter prevents synchronized workers
// from retrying the same upstream at once.
func Exponential(attempt int, base, maximum time.Duration) time.Duration {
	return ExponentialWithJitter(attempt, base, maximum, rand.Float64)
}

// ExponentialWithJitter is deterministic when random is injected, which keeps
// retry tests independent of sleeps and wall-clock timing.
func ExponentialWithJitter(attempt int, base, maximum time.Duration, random func() float64) time.Duration {
	if base <= 0 {
		return 0
	}
	if maximum < base {
		maximum = base
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for index := 1; index < attempt && delay < maximum; index++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if random == nil {
		random = rand.Float64
	}
	sample := random()
	if sample < 0 {
		sample = 0
	}
	if sample > 1 {
		sample = 1
	}
	jittered := time.Duration(float64(delay) * (0.8 + 0.4*sample))
	if jittered < time.Nanosecond {
		jittered = time.Nanosecond
	}
	if jittered > maximum {
		jittered = maximum
	}
	return jittered
}
