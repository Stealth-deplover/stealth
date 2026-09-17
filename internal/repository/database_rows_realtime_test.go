package repository

import (
	"testing"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/google/uuid"
)

func TestDatabaseRowEventMetadataPublishesQueryScope(t *testing.T) {
	databaseID := uuid.Must(uuid.NewV7()).String()
	tableID := uuid.Must(uuid.NewV7()).String()
	metadata := buildDatabaseRowEventMetadata(DatabaseActor{}, domain.DatabaseTable{
		ID:         tableID,
		DatabaseID: databaseID,
	}, nil, []string{"title"})

	if metadata["database_id"] != databaseID || metadata["table_id"] != tableID {
		t.Fatalf("row event scope = %#v", metadata)
	}
	marker, ok := metadata["realtime"].(map[string]any)
	if !ok || marker["database_id"] != databaseID || marker["table_id"] != tableID {
		t.Fatalf("row event permission marker = %#v", metadata["realtime"])
	}
}
