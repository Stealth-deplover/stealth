package repository

import (
	"testing"

	"github.com/google/uuid"
)

func TestRowCursorRoundTripPreservesTextValues(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	for _, value := range []string{
		"abc",
		`"abc`,
		`abc"`,
		`"abc"`,
		`a \"quoted\" value`,
		"日本語 🚀",
		"",
	} {
		encoded := EncodeRowCursor(RowCursor{ID: id, Value: value})
		decoded, err := DecodeRowCursor(encoded)
		if err != nil {
			t.Fatalf("DecodeRowCursor(%q) returned error: %v", value, err)
		}
		if decoded.ID != id || decoded.Value != value {
			t.Fatalf("decoded cursor = %#v, want id=%s value=%q", decoded, id, value)
		}
	}
}
