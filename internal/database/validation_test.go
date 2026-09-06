package database

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizePermissions(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	permissions, err := NormalizePermissions([]string{"users", "user:" + userID.String(), "any"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := permissions[0], "any"; got != want {
		t.Fatalf("first permission = %q, want %q", got, want)
	}
	if _, err := NormalizePermissions([]string{"users", "users"}); !errors.Is(err, ErrDuplicatePermission) {
		t.Fatalf("duplicate permission error = %v", err)
	}
	if _, err := NormalizePermissions([]string{"team:admins"}); !errors.Is(err, ErrInvalidPermissions) {
		t.Fatalf("unknown permission error = %v", err)
	}
}

func TestNormalizeCreateAppliesDefaultsAndTypes(t *testing.T) {
	defaultCount := json.Number("3")
	columns := []ColumnDefinition{
		{Key: "title", Type: TypeVarchar, VarcharSize: intPtr(10), Required: true},
		{Key: "count", Type: TypeInteger, Required: true, Default: defaultCount, HasDefault: true},
	}
	row, err := NormalizeCreate(map[string]any{"title": "hello"}, columns)
	if err != nil {
		t.Fatal(err)
	}
	if row["count"] != defaultCount {
		t.Fatalf("default count = %#v", row["count"])
	}
	if _, err := NormalizeCreate(map[string]any{"title": "too-long-title"}, columns); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("varchar error = %v", err)
	}
	if _, err := NormalizeCreate(map[string]any{"unknown": true}, columns); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestGrants(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	if !Grants([]string{"any"}, Actor{}) {
		t.Fatal("any should grant anonymous")
	}
	if Grants([]string{"users"}, Actor{}) {
		t.Fatal("users should not grant anonymous")
	}
	if !Grants([]string{"users"}, Actor{Authenticated: true, UserID: userID}) {
		t.Fatal("users should grant an authenticated user")
	}
	if !Grants([]string{"user:" + userID.String()}, Actor{Authenticated: true, UserID: userID}) {
		t.Fatal("specific user grant missing")
	}
}

func intPtr(value int) *int { return &value }
