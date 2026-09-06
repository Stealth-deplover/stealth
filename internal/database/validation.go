// Package database contains the deliberately small, dependency-free domain
// rules for the Database core.  SQL and HTTP adapters use these rules so the
// same schema, row and permission semantics apply to every actor.
package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type ColumnType string

const (
	TypeVarchar  ColumnType = "varchar"
	TypeText     ColumnType = "text"
	TypeInteger  ColumnType = "integer"
	TypeDouble   ColumnType = "double"
	TypeBoolean  ColumnType = "boolean"
	TypeDatetime ColumnType = "datetime"
	TypeJSON     ColumnType = "json"
)

var (
	ErrInvalidIdentifier   = errors.New("invalid identifier")
	ErrInvalidName         = errors.New("invalid name")
	ErrInvalidColumn       = errors.New("invalid column")
	ErrInvalidRow          = errors.New("invalid row")
	ErrInvalidPermissions  = errors.New("invalid permissions")
	ErrDuplicatePermission = errors.New("duplicate permission")
	ErrMissingRequired     = errors.New("missing required value")
	ErrUnknownField        = errors.New("unknown field")
	ErrInvalidValue        = errors.New("invalid value")
)

// PostgreSQL identifiers are never generated directly from these strings,
// but keeping keys identifier-shaped makes expression indexes and SDK usage
// predictable. A key may start with an underscore and may be one character;
// human-facing names are validated separately at two characters minimum.
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,119}$`)

type ColumnDefinition struct {
	Key         string
	Type        ColumnType
	Required    bool
	VarcharSize *int
	Default     any
	HasDefault  bool
}

func ValidateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) < 2 || utf8.RuneCountInString(value) > 120 {
		return "", fmt.Errorf("%w: name must be between 2 and 120 characters", ErrInvalidName)
	}
	return value, nil
}

func ValidateIdentifier(value string) (string, error) {
	if !identifierPattern.MatchString(value) {
		return "", fmt.Errorf("%w: must start with a letter or underscore and contain only letters, numbers, and underscores", ErrInvalidIdentifier)
	}
	if strings.EqualFold(value, "id") || strings.EqualFold(value, "table_id") || strings.EqualFold(value, "project_id") || strings.EqualFold(value, "created_at") || strings.EqualFold(value, "updated_at") {
		return "", fmt.Errorf("%w: reserved system field", ErrInvalidIdentifier)
	}
	return value, nil
}

func NormalizePermissions(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, value := range raw {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%w: permission cannot be empty", ErrInvalidPermissions)
		}
		canonical := value
		if value != "any" && value != "users" {
			if !strings.HasPrefix(value, "user:") {
				return nil, fmt.Errorf("%w: %q is not supported", ErrInvalidPermissions, value)
			}
			id, err := uuid.Parse(strings.TrimPrefix(value, "user:"))
			if err != nil {
				return nil, fmt.Errorf("%w: %q is not a project-user permission", ErrInvalidPermissions, value)
			}
			canonical = "user:" + id.String()
		}
		if _, exists := seen[canonical]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicatePermission, canonical)
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	sort.Strings(out)
	return out, nil
}

type Actor struct {
	Authenticated bool
	UserID        uuid.UUID
}

func Grants(permissions []string, actor Actor) bool {
	for _, permission := range permissions {
		switch permission {
		case "any":
			return true
		case "users":
			if actor.Authenticated {
				return true
			}
		default:
			if strings.HasPrefix(permission, "user:") && actor.Authenticated && permission == "user:"+actor.UserID.String() {
				return true
			}
		}
	}
	return false
}

func ValidateColumn(def ColumnDefinition) error {
	if _, err := ValidateIdentifier(def.Key); err != nil {
		return fmt.Errorf("%w: key: %v", ErrInvalidColumn, err)
	}
	switch def.Type {
	case TypeVarchar:
		if def.VarcharSize == nil || *def.VarcharSize < 1 || *def.VarcharSize > 10000 {
			return fmt.Errorf("%w: varchar size must be between 1 and 10000", ErrInvalidColumn)
		}
	case TypeText, TypeInteger, TypeDouble, TypeBoolean, TypeDatetime, TypeJSON:
		if def.VarcharSize != nil {
			return fmt.Errorf("%w: varchar size is only valid for varchar", ErrInvalidColumn)
		}
	default:
		return fmt.Errorf("%w: unsupported column type", ErrInvalidColumn)
	}
	if def.HasDefault {
		if def.Default == nil {
			return fmt.Errorf("%w: defaults cannot be null", ErrInvalidColumn)
		}
		if err := ValidateValue(def, def.Default); err != nil {
			return fmt.Errorf("%w: default: %v", ErrInvalidColumn, err)
		}
	}
	return nil
}

func ValidateValue(def ColumnDefinition, value any) error {
	if value == nil {
		return nil
	}
	switch def.Type {
	case TypeVarchar, TypeText:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: expected string", ErrInvalidValue)
		}
		if def.Type == TypeVarchar && def.VarcharSize != nil && utf8.RuneCountInString(text) > *def.VarcharSize {
			return fmt.Errorf("%w: string exceeds varchar size", ErrInvalidValue)
		}
	case TypeInteger:
		if _, ok := integerValue(value); !ok {
			return fmt.Errorf("%w: expected integer", ErrInvalidValue)
		}
	case TypeDouble:
		if number, ok := numberValue(value); !ok || math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("%w: expected finite number", ErrInvalidValue)
		}
	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%w: expected boolean", ErrInvalidValue)
		}
	case TypeDatetime:
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%w: expected RFC3339 datetime", ErrInvalidValue)
		}
		if _, err := time.Parse(time.RFC3339Nano, text); err != nil {
			return fmt.Errorf("%w: expected RFC3339 datetime", ErrInvalidValue)
		}
	case TypeJSON:
		// Any JSON value is valid for a json column. Request decoding already
		// guarantees that maps/slices/scalars are JSON-compatible.
	default:
		return fmt.Errorf("%w: unsupported column type", ErrInvalidValue)
	}
	return nil
}

// NormalizeCreate applies defaults and validates the complete row. It returns
// a fresh map, so callers can safely retry a transaction without mutating the
// request object.
func NormalizeCreate(data map[string]any, columns []ColumnDefinition) (map[string]any, error) {
	result := make(map[string]any, len(data)+len(columns))
	for key, value := range data {
		result[key] = value
	}
	for key := range result {
		if _, err := ValidateIdentifier(key); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrUnknownField, key)
		}
	}
	byKey := make(map[string]ColumnDefinition, len(columns))
	for _, column := range columns {
		if err := ValidateColumn(column); err != nil {
			return nil, err
		}
		byKey[column.Key] = column
	}
	for key, value := range result {
		column, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownField, key)
		}
		if value == nil {
			if column.Required {
				return nil, fmt.Errorf("%w: %s", ErrMissingRequired, key)
			}
			continue
		}
		if err := ValidateValue(column, value); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
	}
	for _, column := range columns {
		if _, present := result[column.Key]; present {
			continue
		}
		if column.HasDefault {
			result[column.Key] = column.Default
			continue
		}
		if column.Required {
			return nil, fmt.Errorf("%w: %s", ErrMissingRequired, column.Key)
		}
	}
	return result, nil
}

// NormalizeUpdate merges a partial patch with the stored row and validates
// the resulting complete object. Defaults are not re-applied during updates.
func NormalizeUpdate(existing, patch map[string]any, columns []ColumnDefinition) (map[string]any, []string, error) {
	result := make(map[string]any, len(existing)+len(patch))
	for key, value := range existing {
		result[key] = value
	}
	changed := make([]string, 0, len(patch))
	for key, value := range patch {
		if _, err := ValidateIdentifier(key); err != nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrUnknownField, key)
		}
		result[key] = value
		changed = append(changed, key)
	}
	validated, err := normalizeWithoutDefaults(result, columns)
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(changed)
	return validated, changed, nil
}

func normalizeWithoutDefaults(data map[string]any, columns []ColumnDefinition) (map[string]any, error) {
	byKey := make(map[string]ColumnDefinition, len(columns))
	for _, column := range columns {
		if err := ValidateColumn(column); err != nil {
			return nil, err
		}
		byKey[column.Key] = column
	}
	result := make(map[string]any, len(data))
	for key, value := range data {
		column, ok := byKey[key]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownField, key)
		}
		if value == nil {
			if column.Required {
				return nil, fmt.Errorf("%w: %s", ErrMissingRequired, key)
			}
			result[key] = nil
			continue
		}
		if err := ValidateValue(column, value); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		result[key] = value
	}
	for _, column := range columns {
		if column.Required {
			if _, ok := result[column.Key]; !ok {
				return nil, fmt.Errorf("%w: %s", ErrMissingRequired, column.Key)
			}
		}
	}
	return result, nil
}

func ParseQueryValue(def ColumnDefinition, raw string) (any, error) {
	switch def.Type {
	case TypeVarchar, TypeText, TypeDatetime:
		if def.Type == TypeVarchar && def.VarcharSize != nil && utf8.RuneCountInString(raw) > *def.VarcharSize {
			return nil, fmt.Errorf("%w: query value exceeds varchar size", ErrInvalidValue)
		}
		if def.Type == TypeDatetime {
			if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
				return nil, fmt.Errorf("%w: expected RFC3339 datetime", ErrInvalidValue)
			}
		}
		return raw, nil
	case TypeInteger:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: expected integer", ErrInvalidValue)
		}
		return value, nil
	case TypeDouble:
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("%w: expected finite number", ErrInvalidValue)
		}
		return value, nil
	case TypeBoolean:
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: expected boolean", ErrInvalidValue)
		}
		return value, nil
	case TypeJSON:
		var value any
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: expected JSON", ErrInvalidValue)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w: expected one JSON value", ErrInvalidValue)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("%w: unsupported column type", ErrInvalidValue)
	}
}

func integerValue(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), true
	case int8:
		return int64(number), true
	case int16:
		return int64(number), true
	case int32:
		return int64(number), true
	case int64:
		return number, true
	case uint:
		if uint64(number) > math.MaxInt64 {
			return 0, false
		}
		return int64(number), true
	case uint8:
		return int64(number), true
	case uint16:
		return int64(number), true
	case uint32:
		return int64(number), true
	case uint64:
		if number > math.MaxInt64 {
			return 0, false
		}
		return int64(number), true
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < math.MinInt64 || number > math.MaxInt64 {
			return 0, false
		}
		return int64(number), true
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numberValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float32:
		return float64(number), true
	case float64:
		return number, true
	case int:
		return float64(number), true
	case int8:
		return float64(number), true
	case int16:
		return float64(number), true
	case int32:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint:
		return float64(number), true
	case uint8:
		return float64(number), true
	case uint16:
		return float64(number), true
	case uint32:
		return float64(number), true
	case uint64:
		return float64(number), true
	case json.Number:
		parsed, err := strconv.ParseFloat(string(number), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// EncodeQueryValue gives the SQL adapter a stable representation for cursor
// values. Query values are still passed as pgx parameters; URL encoding here
// is only for cursor transport and does not enter SQL.
func EncodeQueryValue(value any) string {
	encoded, _ := json.Marshal(value)
	return url.QueryEscape(string(encoded))
}
