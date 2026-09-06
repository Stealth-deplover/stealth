package repository

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	dbcore "github.com/nazxf/stealth-api/internal/database"
)

func TestBuildFullTextIndexDDL(t *testing.T) {
	tableID := uuid.MustParse("0199fca2-1e2d-7f10-8d9b-3b8b2f9a1e01")
	ddl, err := buildIndexDDL("stealth_db_row_idx_test", tableID, DatabaseIndexInput{Type: "fulltext", ColumnKeys: []string{"title"}}, map[string]DatabaseColumnSchema{
		"title": {Key: "title", Type: dbcore.TypeText},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"USING GIN", "to_tsvector('simple'", "data->>'title'", tableID.String()} {
		if !strings.Contains(ddl, fragment) {
			t.Fatalf("fulltext DDL %q does not contain %q", ddl, fragment)
		}
	}
}

func TestBuildFullTextIndexDDLRejectsNonTextColumns(t *testing.T) {
	_, err := buildIndexDDL("stealth_db_row_idx_test", uuid.MustParse("0199fca2-1e2d-7f10-8d9b-3b8b2f9a1e01"), DatabaseIndexInput{Type: "fulltext", ColumnKeys: []string{"count"}}, map[string]DatabaseColumnSchema{
		"count": {Key: "count", Type: dbcore.TypeInteger},
	})
	if err == nil || !strings.Contains(err.Error(), "fulltext indexes require") {
		t.Fatalf("expected fulltext type validation error, got %v", err)
	}
}
