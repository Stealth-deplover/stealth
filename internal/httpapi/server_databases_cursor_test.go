package httpapi

import (
	"encoding/json"
	"testing"

	dbcore "github.com/Stealth-deplover/stealth/internal/database"
	"github.com/Stealth-deplover/stealth/internal/repository"
)

func TestCanonicalCursorValuePreservesTextBoundaries(t *testing.T) {
	for _, value := range []string{
		"abc",
		`"abc`,
		`abc"`,
		`"abc"`,
		`a \"quoted\" value`,
		"日本語 🚀",
		"",
		"018f27e3-5d1a-7c44-ae35-1db4ea12e6d2",
	} {
		got, err := canonicalCursorValue(repository.DatabaseColumnSchema{
			Key:  "title",
			Type: dbcore.TypeText,
		}, value)
		if err != nil {
			t.Fatalf("canonicalCursorValue(%q) returned error: %v", value, err)
		}
		if got != value {
			t.Fatalf("canonicalCursorValue(%q) = %#v, want exact value", value, got)
		}
	}
}

func TestCanonicalCursorValueNormalizesSupportedScalarTypes(t *testing.T) {
	tests := []struct {
		name   string
		column repository.DatabaseColumnSchema
		value  any
		want   any
	}{
		{
			name:   "integer",
			column: repository.DatabaseColumnSchema{Key: "count", Type: dbcore.TypeInteger},
			value:  json.Number("42"),
			want:   int64(42),
		},
		{
			name:   "double",
			column: repository.DatabaseColumnSchema{Key: "score", Type: dbcore.TypeDouble},
			value:  json.Number("1.25"),
			want:   float64(1.25),
		},
		{
			name:   "boolean",
			column: repository.DatabaseColumnSchema{Key: "enabled", Type: dbcore.TypeBoolean},
			value:  true,
			want:   true,
		},
		{
			name:   "datetime",
			column: repository.DatabaseColumnSchema{Key: "created_at", Type: dbcore.TypeDatetime},
			value:  "2026-09-17T12:34:56.123Z",
			want:   "2026-09-17T12:34:56.123Z",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := canonicalCursorValue(test.column, test.value)
			if err != nil {
				t.Fatalf("canonicalCursorValue returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("canonicalCursorValue = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCanonicalCursorValueRejectsNullOrderedBoundary(t *testing.T) {
	_, err := canonicalCursorValue(repository.DatabaseColumnSchema{
		Key:  "title",
		Type: dbcore.TypeText,
	}, nil)
	if err == nil {
		t.Fatal("canonicalCursorValue accepted a null ordered boundary")
	}
}
