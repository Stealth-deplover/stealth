package retry

import (
	"testing"
	"time"
)

func TestExponentialWithJitterIsBoundedAndDeterministic(t *testing.T) {
	tests := []struct {
		name    string
		attempt int
		random  float64
		want    time.Duration
	}{
		{name: "first low jitter", attempt: 1, random: 0, want: 24 * time.Second},
		{name: "second high jitter", attempt: 2, random: 1, want: 72 * time.Second},
		{name: "cap", attempt: 99, random: 1, want: 24 * time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ExponentialWithJitter(test.attempt, 30*time.Second, 24*time.Hour, func() float64 { return test.random })
			if got != test.want {
				t.Fatalf("delay = %s, want %s", got, test.want)
			}
		})
	}
}

func TestExponentialWithJitterNormalizesInputs(t *testing.T) {
	if got := ExponentialWithJitter(0, time.Second, time.Minute, func() float64 { return 0.5 }); got != time.Second {
		t.Fatalf("normalized attempt delay = %s, want 1s", got)
	}
	if got := ExponentialWithJitter(1, 2*time.Second, time.Second, func() float64 { return 0.5 }); got != 2*time.Second {
		t.Fatalf("maximum below base delay = %s, want 2s", got)
	}
	if got := ExponentialWithJitter(1, 0, time.Minute, func() float64 { return 0.5 }); got != 0 {
		t.Fatalf("zero base delay = %s, want 0", got)
	}
}
