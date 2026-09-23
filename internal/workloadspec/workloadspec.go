// Package workloadspec owns the versioned, runtime-independent contract for
// persistent App workloads. It has no HTTP, database, image, or container
// dependencies so each future build and runtime boundary can share it.
package workloadspec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	SchemaVersionV1 = "v1"

	DefaultPort                            = 8080
	DefaultHealthProtocol                  = "tcp"
	DefaultHealthIntervalSeconds           = 10
	DefaultHealthTimeoutSeconds            = 2
	DefaultHealthInitialDelaySeconds       = 5
	DefaultHealthFailureThreshold          = 3
	DefaultCPUMillis                       = 500
	DefaultMemoryBytes               int64 = 512 * 1024 * 1024
	DefaultPIDsLimit                       = 256
	DefaultStopGracePeriodSeconds          = 15
	DefaultRestartPolicy                   = "always"

	MinPort = 1
	MaxPort = 65535

	MaxCommandArguments             = 64
	MaxCommandArgumentBytes         = 4096
	MaxCommandAggregateBytes        = 32 * 1024
	MaxWorkingDirectoryBytes        = 1024
	MaxHealthPathBytes              = 2048
	MaxSpecJSONBytes                = 256 * 1024
	MinHealthIntervalSeconds        = 5
	MaxHealthIntervalSeconds        = 300
	MinHealthTimeoutSeconds         = 1
	MaxHealthTimeoutSeconds         = 30
	MaxHealthInitialDelay           = 300
	MinHealthFailureThreshold       = 1
	MaxHealthFailureThreshold       = 10
	MinCPUMillis                    = 50
	MaxCPUMillis                    = 8000
	MinMemoryBytes            int64 = 64 * 1024 * 1024
	MaxMemoryBytes            int64 = 16 * 1024 * 1024 * 1024
	MinPIDsLimit                    = 16
	MaxPIDsLimit                    = 2048
	MinStopGracePeriodSeconds       = 1
	MaxStopGracePeriodSeconds       = 120
)

var ErrInvalidSpec = errors.New("invalid workload spec")

// Spec is the public v1 contract. It contains only Stealth-owned workload
// intent; image identity and infrastructure or security options live in
// separate trusted deployment/runtime domains.
type Spec struct {
	SchemaVersion          string      `json:"schema_version"`
	Port                   int         `json:"port"`
	Command                []string    `json:"command"`
	WorkingDirectory       *string     `json:"working_directory"`
	HealthCheck            HealthCheck `json:"health_check"`
	Resources              Resources   `json:"resources"`
	StopGracePeriodSeconds int         `json:"stop_grace_period_seconds"`
	RestartPolicy          string      `json:"restart_policy"`
}

type HealthCheck struct {
	Protocol            string  `json:"protocol"`
	Path                *string `json:"path"`
	IntervalSeconds     int     `json:"interval_seconds"`
	TimeoutSeconds      int     `json:"timeout_seconds"`
	InitialDelaySeconds *int    `json:"initial_delay_seconds"`
	FailureThreshold    int     `json:"failure_threshold"`
}

type Resources struct {
	CPUMillis   int   `json:"cpu_millis"`
	MemoryBytes int64 `json:"memory_bytes"`
	PIDsLimit   int   `json:"pids_limit"`
}

// Default returns the complete, validated WorkloadSpec v1 default.
func Default() Spec {
	spec, _ := Normalize(Spec{})
	return spec
}

// Normalize fills v1 defaults, canonicalizes the in-container working path,
// clones slices, and validates every runtime-relevant value.
func Normalize(input Spec) (Spec, error) {
	spec := input
	if spec.SchemaVersion == "" {
		spec.SchemaVersion = SchemaVersionV1
	}
	if spec.SchemaVersion != SchemaVersionV1 {
		return Spec{}, invalid("schema_version must be v1")
	}
	if spec.Port == 0 {
		spec.Port = DefaultPort
	}
	if spec.Port < MinPort || spec.Port > MaxPort {
		return Spec{}, invalid("port must be between %d and %d", MinPort, MaxPort)
	}
	if spec.Command == nil {
		spec.Command = []string{}
	} else {
		cloned := make([]string, len(spec.Command))
		copy(cloned, spec.Command)
		spec.Command = cloned
	}
	if len(spec.Command) > MaxCommandArguments {
		return Spec{}, invalid("command cannot contain more than %d arguments", MaxCommandArguments)
	}
	aggregate := 0
	for index, argument := range spec.Command {
		if !utf8.ValidString(argument) {
			return Spec{}, invalid("command argument %d must be valid UTF-8", index)
		}
		if strings.ContainsRune(argument, '\x00') {
			return Spec{}, invalid("command argument %d cannot contain NUL", index)
		}
		if len(argument) > MaxCommandArgumentBytes {
			return Spec{}, invalid("command argument %d cannot exceed %d bytes", index, MaxCommandArgumentBytes)
		}
		aggregate += len(argument) + 1
	}
	if aggregate > MaxCommandAggregateBytes {
		return Spec{}, invalid("command cannot exceed %d aggregate bytes", MaxCommandAggregateBytes)
	}

	workingDirectory, err := normalizeWorkingDirectory(spec.WorkingDirectory)
	if err != nil {
		return Spec{}, err
	}
	spec.WorkingDirectory = workingDirectory

	health, err := normalizeHealthCheck(spec.HealthCheck)
	if err != nil {
		return Spec{}, err
	}
	spec.HealthCheck = health

	resources := spec.Resources
	if resources.CPUMillis == 0 {
		resources.CPUMillis = DefaultCPUMillis
	}
	if resources.CPUMillis < MinCPUMillis || resources.CPUMillis > MaxCPUMillis {
		return Spec{}, invalid("resources.cpu_millis must be between %d and %d", MinCPUMillis, MaxCPUMillis)
	}
	if resources.MemoryBytes == 0 {
		resources.MemoryBytes = DefaultMemoryBytes
	}
	if resources.MemoryBytes < MinMemoryBytes || resources.MemoryBytes > MaxMemoryBytes {
		return Spec{}, invalid("resources.memory_bytes must be between %d and %d", MinMemoryBytes, MaxMemoryBytes)
	}
	if resources.PIDsLimit == 0 {
		resources.PIDsLimit = DefaultPIDsLimit
	}
	if resources.PIDsLimit < MinPIDsLimit || resources.PIDsLimit > MaxPIDsLimit {
		return Spec{}, invalid("resources.pids_limit must be between %d and %d", MinPIDsLimit, MaxPIDsLimit)
	}
	spec.Resources = resources

	if spec.StopGracePeriodSeconds == 0 {
		spec.StopGracePeriodSeconds = DefaultStopGracePeriodSeconds
	}
	if spec.StopGracePeriodSeconds < MinStopGracePeriodSeconds || spec.StopGracePeriodSeconds > MaxStopGracePeriodSeconds {
		return Spec{}, invalid("stop_grace_period_seconds must be between %d and %d", MinStopGracePeriodSeconds, MaxStopGracePeriodSeconds)
	}
	if spec.RestartPolicy == "" {
		spec.RestartPolicy = DefaultRestartPolicy
	}
	if spec.RestartPolicy != DefaultRestartPolicy {
		return Spec{}, invalid("restart_policy must be always")
	}
	return spec, nil
}

func normalizeWorkingDirectory(input *string) (*string, error) {
	if input == nil || *input == "" {
		return nil, nil
	}
	value := *input
	if !utf8.ValidString(value) || len(value) > MaxWorkingDirectoryBytes || !strings.HasPrefix(value, "/") {
		return nil, invalid("working_directory must be an absolute POSIX path no longer than %d bytes", MaxWorkingDirectoryBytes)
	}
	for _, character := range value {
		if character == '\\' || unicode.IsControl(character) {
			return nil, invalid("working_directory cannot contain backslashes or control characters")
		}
	}
	canonical := path.Clean(value)
	return &canonical, nil
}

func normalizeHealthCheck(input HealthCheck) (HealthCheck, error) {
	health := input
	if health.Protocol == "" {
		health.Protocol = DefaultHealthProtocol
	}
	if health.IntervalSeconds == 0 {
		health.IntervalSeconds = DefaultHealthIntervalSeconds
	}
	if health.TimeoutSeconds == 0 {
		health.TimeoutSeconds = DefaultHealthTimeoutSeconds
	}
	if health.InitialDelaySeconds == nil {
		initialDelay := DefaultHealthInitialDelaySeconds
		health.InitialDelaySeconds = &initialDelay
	}
	if health.FailureThreshold == 0 {
		health.FailureThreshold = DefaultHealthFailureThreshold
	}
	if health.IntervalSeconds < MinHealthIntervalSeconds || health.IntervalSeconds > MaxHealthIntervalSeconds {
		return HealthCheck{}, invalid("health_check.interval_seconds must be between %d and %d", MinHealthIntervalSeconds, MaxHealthIntervalSeconds)
	}
	if health.TimeoutSeconds < MinHealthTimeoutSeconds || health.TimeoutSeconds > MaxHealthTimeoutSeconds || health.TimeoutSeconds > health.IntervalSeconds {
		return HealthCheck{}, invalid("health_check.timeout_seconds must be between %d and %d and no greater than interval_seconds", MinHealthTimeoutSeconds, MaxHealthTimeoutSeconds)
	}
	if *health.InitialDelaySeconds < 0 || *health.InitialDelaySeconds > MaxHealthInitialDelay {
		return HealthCheck{}, invalid("health_check.initial_delay_seconds must be between 0 and %d", MaxHealthInitialDelay)
	}
	if health.FailureThreshold < MinHealthFailureThreshold || health.FailureThreshold > MaxHealthFailureThreshold {
		return HealthCheck{}, invalid("health_check.failure_threshold must be between %d and %d", MinHealthFailureThreshold, MaxHealthFailureThreshold)
	}
	switch health.Protocol {
	case "tcp":
		if health.Path != nil {
			return HealthCheck{}, invalid("health_check.path must be null when protocol is tcp")
		}
	case "http":
		if health.Path == nil {
			return HealthCheck{}, invalid("health_check.path is required when protocol is http")
		}
		if err := validateHTTPPath(*health.Path); err != nil {
			return HealthCheck{}, err
		}
	default:
		return HealthCheck{}, invalid("health_check.protocol must be tcp or http")
	}
	if health.Path != nil {
		value := *health.Path
		health.Path = &value
	}
	initialDelay := *health.InitialDelaySeconds
	health.InitialDelaySeconds = &initialDelay
	return health, nil
}

func validateHTTPPath(value string) error {
	if len(value) == 0 || len(value) > MaxHealthPathBytes || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return invalid("health_check.path must be an absolute local path no longer than %d bytes", MaxHealthPathBytes)
	}
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\\?#") {
		return invalid("health_check.path cannot contain a scheme, query, fragment, or backslash")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return invalid("health_check.path cannot contain control characters")
		}
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
		return invalid("health_check.path must be a local path without a host or credentials")
	}
	return nil
}

// Decode strictly decodes a partial v1 WorkloadSpec and normalizes it. At
// least one field must be present when callers provide a spec object; callers
// that want the complete default use Default instead. Omitted schema_version
// is explicitly materialized as v1 in the normalized result.
func Decode(data []byte) (Spec, error) {
	if len(data) == 0 || len(data) > MaxSpecJSONBytes {
		return Spec{}, invalid("workload spec JSON must be between 1 and %d bytes", MaxSpecJSONBytes)
	}
	var input partialSpec
	if err := decodeSingleJSON(data, &input); err != nil {
		return Spec{}, err
	}
	if !input.hasAnyField() {
		return Spec{}, invalid("workload spec must contain at least one field")
	}

	var spec Spec
	if err := decodeRequiredField(input.SchemaVersion, "schema_version", &spec.SchemaVersion); err != nil {
		return Spec{}, err
	}
	if input.SchemaVersion != nil && spec.SchemaVersion == "" {
		return Spec{}, invalid("schema_version must be v1 when provided")
	}
	if err := decodeRequiredField(input.Port, "port", &spec.Port); err != nil {
		return Spec{}, err
	}
	if input.Port != nil && spec.Port == 0 {
		return Spec{}, invalid("port must be between %d and %d", MinPort, MaxPort)
	}
	if err := decodeRequiredField(input.Command, "command", &spec.Command); err != nil {
		return Spec{}, err
	}
	if err := decodeNullableField(input.WorkingDirectory, "working_directory", &spec.WorkingDirectory); err != nil {
		return Spec{}, err
	}
	if input.HealthCheck != nil {
		var health partialHealthCheck
		if err := decodeSingleJSON(input.HealthCheck, &health); err != nil {
			return Spec{}, err
		}
		if bytes.Equal(bytes.TrimSpace(input.HealthCheck), []byte("null")) {
			return Spec{}, invalid("health_check must be an object")
		}
		if err := decodeRequiredField(health.Protocol, "health_check.protocol", &spec.HealthCheck.Protocol); err != nil {
			return Spec{}, err
		}
		if err := decodeNullableField(health.Path, "health_check.path", &spec.HealthCheck.Path); err != nil {
			return Spec{}, err
		}
		if err := decodeRequiredField(health.IntervalSeconds, "health_check.interval_seconds", &spec.HealthCheck.IntervalSeconds); err != nil {
			return Spec{}, err
		}
		if err := decodeRequiredField(health.TimeoutSeconds, "health_check.timeout_seconds", &spec.HealthCheck.TimeoutSeconds); err != nil {
			return Spec{}, err
		}
		var initialDelay int
		if err := decodeRequiredField(health.InitialDelaySeconds, "health_check.initial_delay_seconds", &initialDelay); err != nil {
			return Spec{}, err
		}
		if health.InitialDelaySeconds != nil {
			spec.HealthCheck.InitialDelaySeconds = &initialDelay
		}
		if err := decodeRequiredField(health.FailureThreshold, "health_check.failure_threshold", &spec.HealthCheck.FailureThreshold); err != nil {
			return Spec{}, err
		}
		if health.Protocol != nil && spec.HealthCheck.Protocol == "" {
			return Spec{}, invalid("health_check.protocol cannot be empty")
		}
		if err := rejectExplicitZero(health.IntervalSeconds, spec.HealthCheck.IntervalSeconds, "health_check.interval_seconds"); err != nil {
			return Spec{}, err
		}
		if err := rejectExplicitZero(health.TimeoutSeconds, spec.HealthCheck.TimeoutSeconds, "health_check.timeout_seconds"); err != nil {
			return Spec{}, err
		}
		if err := rejectExplicitZero(health.FailureThreshold, spec.HealthCheck.FailureThreshold, "health_check.failure_threshold"); err != nil {
			return Spec{}, err
		}
	}
	if input.Resources != nil {
		var resources partialResources
		if err := decodeSingleJSON(input.Resources, &resources); err != nil {
			return Spec{}, err
		}
		if bytes.Equal(bytes.TrimSpace(input.Resources), []byte("null")) {
			return Spec{}, invalid("resources must be an object")
		}
		if err := decodeRequiredField(resources.CPUMillis, "resources.cpu_millis", &spec.Resources.CPUMillis); err != nil {
			return Spec{}, err
		}
		if err := decodeRequiredField(resources.MemoryBytes, "resources.memory_bytes", &spec.Resources.MemoryBytes); err != nil {
			return Spec{}, err
		}
		if err := decodeRequiredField(resources.PIDsLimit, "resources.pids_limit", &spec.Resources.PIDsLimit); err != nil {
			return Spec{}, err
		}
		if err := rejectExplicitZero(resources.CPUMillis, spec.Resources.CPUMillis, "resources.cpu_millis"); err != nil {
			return Spec{}, err
		}
		if err := rejectExplicitZeroInt64(resources.MemoryBytes, spec.Resources.MemoryBytes, "resources.memory_bytes"); err != nil {
			return Spec{}, err
		}
		if err := rejectExplicitZero(resources.PIDsLimit, spec.Resources.PIDsLimit, "resources.pids_limit"); err != nil {
			return Spec{}, err
		}
	}
	if err := decodeRequiredField(input.StopGracePeriodSeconds, "stop_grace_period_seconds", &spec.StopGracePeriodSeconds); err != nil {
		return Spec{}, err
	}
	if input.StopGracePeriodSeconds != nil && spec.StopGracePeriodSeconds == 0 {
		return Spec{}, invalid("stop_grace_period_seconds must be between %d and %d", MinStopGracePeriodSeconds, MaxStopGracePeriodSeconds)
	}
	if err := decodeRequiredField(input.RestartPolicy, "restart_policy", &spec.RestartPolicy); err != nil {
		return Spec{}, err
	}
	if input.RestartPolicy != nil && spec.RestartPolicy == "" {
		return Spec{}, invalid("restart_policy cannot be empty")
	}
	return Normalize(spec)
}

type partialSpec struct {
	SchemaVersion          json.RawMessage `json:"schema_version"`
	Port                   json.RawMessage `json:"port"`
	Command                json.RawMessage `json:"command"`
	WorkingDirectory       json.RawMessage `json:"working_directory"`
	HealthCheck            json.RawMessage `json:"health_check"`
	Resources              json.RawMessage `json:"resources"`
	StopGracePeriodSeconds json.RawMessage `json:"stop_grace_period_seconds"`
	RestartPolicy          json.RawMessage `json:"restart_policy"`
}

func (input partialSpec) hasAnyField() bool {
	return input.SchemaVersion != nil || input.Port != nil || input.Command != nil || input.WorkingDirectory != nil || input.HealthCheck != nil || input.Resources != nil || input.StopGracePeriodSeconds != nil || input.RestartPolicy != nil
}

type partialHealthCheck struct {
	Protocol            json.RawMessage `json:"protocol"`
	Path                json.RawMessage `json:"path"`
	IntervalSeconds     json.RawMessage `json:"interval_seconds"`
	TimeoutSeconds      json.RawMessage `json:"timeout_seconds"`
	InitialDelaySeconds json.RawMessage `json:"initial_delay_seconds"`
	FailureThreshold    json.RawMessage `json:"failure_threshold"`
}

type partialResources struct {
	CPUMillis   json.RawMessage `json:"cpu_millis"`
	MemoryBytes json.RawMessage `json:"memory_bytes"`
	PIDsLimit   json.RawMessage `json:"pids_limit"`
}

func decodeSingleJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalid("workload spec contains invalid JSON: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalid("workload spec must contain exactly one JSON value")
	}
	return nil
}

func decodeRequiredField[T any](raw json.RawMessage, field string, target *T) error {
	if raw == nil {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return invalid("%s cannot be null", field)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return invalid("%s has the wrong JSON type", field)
	}
	return nil
}

func decodeNullableField[T any](raw json.RawMessage, field string, target **T) error {
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return invalid("%s has the wrong JSON type", field)
	}
	*target = &value
	return nil
}

func rejectExplicitZero(raw json.RawMessage, value int, field string) error {
	if raw != nil && value == 0 {
		return invalid("%s cannot be zero when provided", field)
	}
	return nil
}

func rejectExplicitZeroInt64(raw json.RawMessage, value int64, field string) error {
	if raw != nil && value == 0 {
		return invalid("%s cannot be zero when provided", field)
	}
	return nil
}

// MarshalCanonical serializes the normalized spec using a struct with a fixed
// field order and no maps. It always includes every v1 field and explicit
// nulls/empty arrays, so equivalent inputs have identical bytes.
func MarshalCanonical(input Spec) ([]byte, error) {
	spec, err := Normalize(input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(spec)
}

// Digest returns the lowercase hexadecimal SHA-256 of the canonical v1 JSON.
func Digest(input Spec) (string, error) {
	canonical, err := MarshalCanonical(input)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Equal compares two specs after normalization using their canonical bytes.
func Equal(left, right Spec) bool {
	leftBytes, leftErr := MarshalCanonical(left)
	rightBytes, rightErr := MarshalCanonical(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func invalid(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSpec, fmt.Sprintf(format, values...))
}
