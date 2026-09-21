package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestAdminRealtimeCursorV2RoundTrip(t *testing.T) {
	request := httptest.NewRequest("GET", "/v1/admin/realtime", nil)
	want := int64(12345)
	request.Header.Set("Last-Event-ID", encodeAdminRealtimeCursor(want))

	got, err := decodeAdminRealtimeCursor(request)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("cursor = %v, want %d", got, want)
	}
}

func TestAdminRealtimeCursorRejectsMalformedV2Values(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload any
	}{
		{name: "unknown version", payload: map[string]any{"v": 3, "sequence": 1}},
		{name: "zero sequence", payload: map[string]any{"v": adminRealtimeCursorVersion, "sequence": 0}},
		{name: "negative sequence", payload: map[string]any{"v": adminRealtimeCursorVersion, "sequence": -1}},
		{name: "extra field", payload: map[string]any{"v": adminRealtimeCursorVersion, "sequence": 1, "extra": true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("GET", "/v1/admin/realtime", nil)
			request.Header.Set("Last-Event-ID", base64.RawURLEncoding.EncodeToString(encoded))
			if _, err := decodeAdminRealtimeCursor(request); err == nil {
				t.Fatal("malformed v2 cursor was accepted")
			}
		})
	}

	request := httptest.NewRequest("GET", "/v1/admin/realtime", nil)
	request.Header.Set("Last-Event-ID", "not-base64-json")
	if _, err := decodeAdminRealtimeCursor(request); err == nil {
		t.Fatal("malformed base64 cursor was accepted")
	}
}

func TestAdminRealtimeCursorTreatsLegacyUUIDAsStale(t *testing.T) {
	request := httptest.NewRequest("GET", "/v1/admin/realtime", nil)
	request.Header.Set("Last-Event-ID", uuid.Must(uuid.NewV7()).String())
	if got, err := decodeAdminRealtimeCursor(request); err != nil || got != nil {
		t.Fatalf("legacy UUID cursor = %v, err = %v; want safe fresh-tail behavior", got, err)
	}
}
