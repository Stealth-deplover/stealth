package main

import "testing"

func TestRuntimeConfigurationVersionTracksSecretReplacementWithoutChangingMode(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		secret string
		want   string
	}{
		{name: "initial configuration", mode: "v1", secret: "fake-smoke-secret-not-real-v1", want: "v1"},
		{name: "rotated secret with unchanged variable", mode: "v1", secret: "fake-smoke-secret-not-real-v2", want: "v2"},
		{name: "unrequested variable mutation", mode: "v2", secret: "fake-smoke-secret-not-real-v2"},
		{name: "old secret", mode: "v1", secret: "fake-smoke-secret-not-real-v0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := runtimeConfigurationVersion(test.mode, test.secret); got != test.want {
				t.Fatalf("runtimeConfigurationVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
