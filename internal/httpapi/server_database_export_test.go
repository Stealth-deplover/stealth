package httpapi

import (
	"errors"
	"net/http/httptest"
	"testing"

	dbcore "github.com/nazxf/stealth-api/internal/database"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestParseDatabaseExportLimit(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "default", want: repository.DatabaseRowExportDefaultLimit},
		{name: "trimmed", value: " 25 ", want: 25},
		{name: "minimum", value: "1", want: 1},
		{name: "maximum", value: "10000", want: 10000},
		{name: "zero", value: "0", wantErr: true},
		{name: "too large", value: "10001", wantErr: true},
		{name: "not a number", value: "many", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseDatabaseExportLimit(test.value)
			if test.wantErr {
				if err == nil || !errors.Is(err, repository.ErrInvalidQuery) {
					t.Fatalf("expected invalid query error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Fatalf("limit = %d, want %d", got, test.want)
			}
		})
	}
}

func TestParseRowQueryFullTextSearch(t *testing.T) {
	schema := repository.DatabaseTableSchema{Columns: []repository.DatabaseColumnSchema{
		{Key: "title", Type: dbcore.TypeText},
		{Key: "count", Type: dbcore.TypeInteger},
	}}
	request := httptest.NewRequest("GET", "/?limit=10&search=hello+world&search_column=title", nil)
	query, err := parseRowQuery(request, schema)
	if err != nil {
		t.Fatal(err)
	}
	if query.Search != "hello world" || query.SearchColumn == nil || query.SearchColumn.Key != "title" {
		t.Fatalf("full-text query = %#v", query)
	}
	for _, rawURL := range []string{
		"/?search=hello",
		"/?search_column=title",
		"/?search=hello&search_column=count",
	} {
		if _, err := parseRowQuery(httptest.NewRequest("GET", rawURL, nil), schema); err == nil || !errors.Is(err, repository.ErrInvalidQuery) {
			t.Fatalf("parseRowQuery(%s) error = %v", rawURL, err)
		}
	}
}
