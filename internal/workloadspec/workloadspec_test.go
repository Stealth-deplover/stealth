package workloadspec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNormalizeDefaultsAndCanonicalJSON(t *testing.T) {
	spec, err := Normalize(Spec{})
	if err != nil {
		t.Fatal(err)
	}
	want := Spec{
		SchemaVersion: SchemaVersionV1,
		Port:          DefaultPort,
		Command:       []string{},
		HealthCheck: HealthCheck{
			Protocol:            DefaultHealthProtocol,
			IntervalSeconds:     DefaultHealthIntervalSeconds,
			TimeoutSeconds:      DefaultHealthTimeoutSeconds,
			InitialDelaySeconds: intPointer(DefaultHealthInitialDelaySeconds),
			FailureThreshold:    DefaultHealthFailureThreshold,
		},
		Resources: Resources{
			CPUMillis:   DefaultCPUMillis,
			MemoryBytes: DefaultMemoryBytes,
			PIDsLimit:   DefaultPIDsLimit,
		},
		StopGracePeriodSeconds: DefaultStopGracePeriodSeconds,
		RestartPolicy:          DefaultRestartPolicy,
	}
	if !Equal(spec, want) {
		t.Fatalf("Normalize(Spec{}) = %#v, want %#v", spec, want)
	}
	canonical, err := MarshalCanonical(spec)
	if err != nil {
		t.Fatal(err)
	}
	const expected = `{"schema_version":"v1","port":8080,"command":[],"working_directory":null,"health_check":{"protocol":"tcp","path":null,"interval_seconds":10,"timeout_seconds":2,"initial_delay_seconds":5,"failure_threshold":3},"resources":{"cpu_millis":500,"memory_bytes":536870912,"pids_limit":256},"stop_grace_period_seconds":15,"restart_policy":"always"}`
	if string(canonical) != expected {
		t.Fatalf("canonical JSON = %s, want %s", canonical, expected)
	}
}

func TestNormalizeCanonicalizesPathAndClonesCommand(t *testing.T) {
	command := []string{"./server", "--port", "8080"}
	workingDirectory := "/srv//app/../app"
	spec, err := Normalize(Spec{Command: command, WorkingDirectory: &workingDirectory})
	if err != nil {
		t.Fatal(err)
	}
	if spec.WorkingDirectory == nil || *spec.WorkingDirectory != "/srv/app" {
		t.Fatalf("working directory = %v, want /srv/app", spec.WorkingDirectory)
	}
	command[0] = "mutated"
	if spec.Command[0] != "./server" {
		t.Fatalf("normalized command aliases caller slice: %#v", spec.Command)
	}
	if normalized, err := Normalize(Spec{WorkingDirectory: stringPointer("")}); err != nil || normalized.WorkingDirectory != nil {
		t.Fatalf("empty working directory = %#v, %v; want null", normalized.WorkingDirectory, err)
	}
}

func TestDigestIsStableAndUsesCanonicalNormalizedJSON(t *testing.T) {
	left, err := Decode([]byte(`{"port":8080,"command":[],"working_directory":"/srv//app/../app"}`))
	if err != nil {
		t.Fatal(err)
	}
	right, err := Decode([]byte(`{"schema_version":"v1","working_directory":"/srv/app","port":8080,"command":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	leftDigest, err := Digest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := Digest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest || len(leftDigest) != 64 || strings.ToLower(leftDigest) != leftDigest {
		t.Fatalf("equivalent digest values = %q and %q", leftDigest, rightDigest)
	}
	canonical, err := MarshalCanonical(left)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	if got := hex.EncodeToString(sum[:]); got != leftDigest {
		t.Fatalf("digest = %q, SHA-256(canonical JSON) = %q", leftDigest, got)
	}
}

func TestDecodeRejectsUnknownFieldsVersionsEmptyObjectsAndTrailingJSON(t *testing.T) {
	for _, raw := range []string{
		`{"port":8080,"image":"tenant-image"}`,
		`{"port":8080,"health_check":{"protocol":"tcp","path":null,"port":1234}}`,
		`{"port":8080,"resources":{"cpu_millis":500,"privileged":true}}`,
		`{"schema_version":"v2","port":8080}`,
		`{"schema_version":"future","port":8080}`,
		`{"schema_version":"latest","port":8080}`,
		`{"schema_version":"","port":8080}`,
		`{}`,
		`{"port":8080} {"port":9000}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Decode([]byte(raw)); !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("Decode(%s) error = %v, want ErrInvalidSpec", raw, err)
			}
		})
	}
	if _, err := Decode([]byte(strings.Repeat(" ", MaxSpecJSONBytes+1))); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("oversized input error = %v", err)
	}
}

func TestDecodeRejectsNullForNonNullableFields(t *testing.T) {
	for _, raw := range []string{
		`{"port":null}`,
		`{"command":null}`,
		`{"health_check":null}`,
		`{"resources":null}`,
		`{"restart_policy":null}`,
	} {
		if _, err := Decode([]byte(raw)); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("Decode(%s) error = %v, want ErrInvalidSpec", raw, err)
		}
	}
}

func TestPortBoundsAndExplicitZero(t *testing.T) {
	for _, test := range []struct {
		port  int
		valid bool
	}{
		{port: MinPort, valid: true},
		{port: MaxPort, valid: true},
		{port: -1},
		{port: MaxPort + 1},
	} {
		spec, err := Normalize(Spec{Port: test.port})
		if (err == nil) != test.valid {
			t.Fatalf("Normalize(port=%d) error = %v, valid = %t", test.port, err, test.valid)
		}
		if err == nil && spec.Port != test.port {
			t.Fatalf("normalized port = %d, want %d", spec.Port, test.port)
		}
	}
	if _, err := Decode([]byte(`{"port":0}`)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("explicit zero port error = %v", err)
	}
	if Default().Port != DefaultPort {
		t.Fatalf("default port = %d", Default().Port)
	}
}

func TestCommandBoundsAndExecArraySemantics(t *testing.T) {
	tooMany := make([]string, MaxCommandArguments+1)
	if _, err := Normalize(Spec{Command: tooMany}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("too many arguments error = %v", err)
	}
	if _, err := Normalize(Spec{Command: []string{strings.Repeat("a", MaxCommandArgumentBytes+1)}}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("oversized argument error = %v", err)
	}
	if _, err := Normalize(Spec{Command: []string{strings.Repeat("a", MaxCommandAggregateBytes)}}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("oversized aggregate error = %v", err)
	}
	if _, err := Normalize(Spec{Command: []string{"server\x00--unsafe"}}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("NUL argument error = %v", err)
	}
	if _, err := Decode([]byte(`{"command":"./server --port 8080"}`)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("shell command string error = %v", err)
	}
	if _, err := Decode([]byte(`{"command":[]}`)); err != nil {
		t.Fatalf("empty exec argument array rejected: %v", err)
	}
}

func TestWorkingDirectorySafety(t *testing.T) {
	for _, value := range []string{
		"relative/path",
		"/srv\\app",
		"/srv/app\x00",
		"/srv/app\n",
		"/" + strings.Repeat("a", MaxWorkingDirectoryBytes),
	} {
		if _, err := Normalize(Spec{WorkingDirectory: &value}); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("unsafe working directory %q error = %v", value, err)
		}
	}
	root := "/"
	if spec, err := Normalize(Spec{WorkingDirectory: &root}); err != nil || spec.WorkingDirectory == nil || *spec.WorkingDirectory != "/" {
		t.Fatalf("root working directory = %#v, %v", spec.WorkingDirectory, err)
	}
}

func TestTCPHealthCheckRequiresNullPath(t *testing.T) {
	for _, path := range []*string{stringPointer(""), stringPointer("/healthz")} {
		if _, err := Normalize(Spec{HealthCheck: HealthCheck{Protocol: "tcp", Path: path}}); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("TCP path %v error = %v", path, err)
		}
	}
	if _, err := Normalize(Spec{HealthCheck: HealthCheck{Protocol: "tcp"}}); err != nil {
		t.Fatalf("TCP null path rejected: %v", err)
	}
}

func TestHTTPHealthPathValidation(t *testing.T) {
	valid := "/healthz"
	if _, err := Normalize(Spec{HealthCheck: HealthCheck{Protocol: "http", Path: &valid}}); err != nil {
		t.Fatalf("valid HTTP health path rejected: %v", err)
	}
	for _, value := range []string{
		"",
		"healthz",
		"//example.test/healthz",
		"https://example.test/healthz",
		"/healthz#ready",
		"/healthz?token=x",
		"/healthz\\other",
		"/healthz\rcheck",
		"/" + strings.Repeat("a", MaxHealthPathBytes),
	} {
		if err := validateHTTPPath(value); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("validateHTTPPath(%q) error = %v", value, err)
		}
	}
	if _, err := Normalize(Spec{HealthCheck: HealthCheck{Protocol: "http"}}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("missing HTTP path error = %v", err)
	}
}

func TestHealthTimingBounds(t *testing.T) {
	for _, health := range []HealthCheck{
		{Protocol: "tcp", IntervalSeconds: MinHealthIntervalSeconds - 1},
		{Protocol: "tcp", IntervalSeconds: MaxHealthIntervalSeconds + 1},
		{Protocol: "tcp", TimeoutSeconds: -1},
		{Protocol: "tcp", TimeoutSeconds: MaxHealthTimeoutSeconds + 1},
		{Protocol: "tcp", IntervalSeconds: 5, TimeoutSeconds: 6},
		{Protocol: "tcp", InitialDelaySeconds: intPointer(-1)},
		{Protocol: "tcp", InitialDelaySeconds: intPointer(MaxHealthInitialDelay + 1)},
		{Protocol: "tcp", FailureThreshold: -1},
		{Protocol: "tcp", FailureThreshold: MaxHealthFailureThreshold + 1},
	} {
		if _, err := Normalize(Spec{HealthCheck: health}); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("invalid health values %#v error = %v", health, err)
		}
	}
	for _, raw := range []string{
		`{"health_check":{"interval_seconds":0}}`,
		`{"health_check":{"timeout_seconds":0}}`,
		`{"health_check":{"failure_threshold":0}}`,
	} {
		if _, err := Decode([]byte(raw)); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("Decode(%s) error = %v, want ErrInvalidSpec", raw, err)
		}
	}
	if spec, err := Decode([]byte(`{"health_check":{"initial_delay_seconds":0}}`)); err != nil || spec.HealthCheck.InitialDelaySeconds == nil || *spec.HealthCheck.InitialDelaySeconds != 0 {
		t.Fatalf("explicit zero initial delay = %v, %v", spec.HealthCheck.InitialDelaySeconds, err)
	}
}

func TestResourceBounds(t *testing.T) {
	for _, resources := range []Resources{
		{CPUMillis: MinCPUMillis - 1},
		{CPUMillis: MaxCPUMillis + 1},
		{MemoryBytes: MinMemoryBytes - 1},
		{MemoryBytes: MaxMemoryBytes + 1},
		{PIDsLimit: MinPIDsLimit - 1},
		{PIDsLimit: MaxPIDsLimit + 1},
	} {
		if _, err := Normalize(Spec{Resources: resources}); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("invalid resources %#v error = %v", resources, err)
		}
	}
	for _, raw := range []string{
		`{"resources":{"cpu_millis":0}}`,
		`{"resources":{"memory_bytes":0}}`,
		`{"resources":{"pids_limit":0}}`,
	} {
		if _, err := Decode([]byte(raw)); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("Decode(%s) error = %v, want ErrInvalidSpec", raw, err)
		}
	}
	if got := Default().Resources; got.CPUMillis != 500 || got.MemoryBytes != 536870912 || got.PIDsLimit != 256 {
		t.Fatalf("default resources = %#v", got)
	}
}

func TestStopGracePeriodAndRestartPolicy(t *testing.T) {
	for _, seconds := range []int{-1, MaxStopGracePeriodSeconds + 1} {
		if _, err := Normalize(Spec{StopGracePeriodSeconds: seconds}); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("stop grace %d error = %v", seconds, err)
		}
	}
	if _, err := Decode([]byte(`{"stop_grace_period_seconds":0}`)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("explicit zero stop grace error = %v", err)
	}
	for _, policy := range []string{"unless-stopped", "on-failure:5", "no"} {
		if _, err := Normalize(Spec{RestartPolicy: policy}); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("restart policy %q error = %v", policy, err)
		}
	}
	if _, err := Decode([]byte(`{"restart_policy":""}`)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("explicit empty restart policy error = %v", err)
	}
	if got := Default().RestartPolicy; got != "always" {
		t.Fatalf("default restart policy = %q", got)
	}
}

func TestCanonicalRoundTripIsStable(t *testing.T) {
	input, err := Decode([]byte(`{"schema_version":"v1","port":9090,"command":["./server","--port","9090"],"working_directory":"/srv//app","health_check":{"protocol":"http","path":"/healthz"},"resources":{"cpu_millis":750,"memory_bytes":1073741824,"pids_limit":128}}`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := MarshalCanonical(input)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalCanonical(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !Equal(input, roundTrip) {
		t.Fatalf("canonical round trip changed: %s != %s", first, second)
	}
	firstDigest, err := Digest(input)
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := Digest(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("round-trip digest changed: %s != %s", firstDigest, secondDigest)
	}
}

func FuzzDecodeNoPanicAndStableRoundTrip(f *testing.F) {
	for _, seed := range []string{
		`{"port":8080}`,
		`{"command":["./server","--port","8080"]}`,
		`{"health_check":{"protocol":"http","path":"/healthz"}}`,
		`{"schema_version":"v2"}`,
		`{"port":0}`, `null`, `{}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > MaxSpecJSONBytes {
			return
		}
		spec, err := Decode([]byte(raw))
		if err != nil {
			return
		}
		canonical, err := MarshalCanonical(spec)
		if err != nil {
			t.Fatalf("accepted spec could not marshal: %v", err)
		}
		roundTrip, err := Decode(canonical)
		if err != nil {
			t.Fatalf("canonical JSON did not decode: %v", err)
		}
		roundTripBytes, err := MarshalCanonical(roundTrip)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical, roundTripBytes) {
			t.Fatalf("canonical round trip is unstable: %s != %s", canonical, roundTripBytes)
		}
		firstDigest, err := Digest(spec)
		if err != nil {
			t.Fatal(err)
		}
		secondDigest, err := Digest(roundTrip)
		if err != nil || firstDigest != secondDigest {
			t.Fatalf("round trip digest = %s, %v; want %s", secondDigest, err, firstDigest)
		}
	})
}

func FuzzHealthPathValidationNoPanic(f *testing.F) {
	for _, seed := range []string{"/healthz", "//outside.test/", "https://outside.test", "/a#b", "/a\\b", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > MaxHealthPathBytes+1 {
			return
		}
		_ = validateHTTPPath(value)
	})
}

func FuzzCanonicalMarshalRoundTrip(f *testing.F) {
	f.Add(8080, "./server", "/srv/app", "/healthz")
	f.Add(9090, "worker", "/", "/ready")
	f.Fuzz(func(t *testing.T, port int, argument, directory, healthPath string) {
		if len(argument) > MaxCommandArgumentBytes || len(directory) > MaxWorkingDirectoryBytes || len(healthPath) > MaxHealthPathBytes {
			return
		}
		workingDirectory := directory
		pathValue := healthPath
		spec, err := Normalize(Spec{
			Port:             port,
			Command:          []string{argument},
			WorkingDirectory: &workingDirectory,
			HealthCheck:      HealthCheck{Protocol: "http", Path: &pathValue},
		})
		if err != nil {
			return
		}
		canonical, err := MarshalCanonical(spec)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := Decode(canonical)
		if err != nil {
			t.Fatalf("accepted canonical JSON did not decode: %v", err)
		}
		reencoded, err := json.Marshal(roundTrip)
		if err != nil || !bytes.Equal(canonical, reencoded) {
			t.Fatalf("canonical JSON changed after round trip: %s != %s, %v", canonical, reencoded, err)
		}
	})
}

func stringPointer(value string) *string { return &value }

func intPointer(value int) *int { return &value }

func ExampleDefault() {
	canonical, _ := MarshalCanonical(Default())
	fmt.Println(string(canonical))
	// Output: {"schema_version":"v1","port":8080,"command":[],"working_directory":null,"health_check":{"protocol":"tcp","path":null,"interval_seconds":10,"timeout_seconds":2,"initial_delay_seconds":5,"failure_threshold":3},"resources":{"cpu_millis":500,"memory_bytes":536870912,"pids_limit":256},"stop_grace_period_seconds":15,"restart_policy":"always"}
}
