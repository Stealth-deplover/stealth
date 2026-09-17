package preflight

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestResourceChecksShareRecommendedThresholdsAndProbeSeams(t *testing.T) {
	var checkedPath string
	checks := ResourceChecks(context.Background(), Probes{
		CPUCount: func() int { return RecommendedCPUs },
		MemoryBytes: func() (uint64, error) {
			return RecommendedMemoryBytes, nil
		},
		FreeBytes: func(path string) (uint64, error) {
			checkedPath = path
			return RecommendedDiskBytes, nil
		},
	}, "/path/that/does/not/exist")
	if len(checks) != 3 || !ChecksPass(checks) {
		t.Fatalf("resource checks = %#v", checks)
	}
	if checkedPath == "/path/that/does/not/exist" {
		t.Fatal("resource checks did not resolve an existing filesystem path")
	}
	if checks[0].Name != "CPU" || checks[1].Name != "Memory" || checks[2].Name != "Disk" {
		t.Fatalf("resource check order = %#v", checks)
	}
}

func TestDockerChecksUseOneSharedDefinition(t *testing.T) {
	checks := DockerChecks(context.Background(), Probes{
		DockerVersion:   func(context.Context) (string, error) { return "27.0", nil },
		ComposeVersion:  func(context.Context) (string, error) { return "v2.30", nil },
		DockerSocketGID: func(string) (uint32, error) { return 998, nil },
	}, true, true)
	if len(checks) != 3 || !ChecksPass(checks) {
		t.Fatalf("Docker checks = %#v", checks)
	}
	if checks[0].Name != "Docker" || checks[1].Name != "Docker Compose" || checks[2].Name != "Docker socket" {
		t.Fatalf("Docker check order = %#v", checks)
	}
	if !strings.Contains(RenderCheck(checks[0]), "27.0") {
		t.Fatalf("rendered Docker check = %q", RenderCheck(checks[0]))
	}
}

func TestHostPreflightReportsUnavailableDockerAndInsufficientResources(t *testing.T) {
	dockerChecks := DockerChecks(context.Background(), Probes{
		DockerVersion:   func(context.Context) (string, error) { return "", errors.New("daemon is inaccessible") },
		ComposeVersion:  func(context.Context) (string, error) { return "", errors.New("compose is missing") },
		DockerSocketGID: func(string) (uint32, error) { return 0, errors.New("socket is inaccessible") },
	}, true, true)
	if len(dockerChecks) != 3 {
		t.Fatalf("Docker failure checks = %#v", dockerChecks)
	}
	for _, check := range dockerChecks {
		if check.OK || !check.Required {
			t.Fatalf("Docker failure check = %#v", check)
		}
	}

	resourceChecks := ResourceChecks(context.Background(), Probes{
		CPUCount:    func() int { return RecommendedCPUs - 1 },
		MemoryBytes: func() (uint64, error) { return RecommendedMemoryBytes - 1, nil },
		FreeBytes:   func(string) (uint64, error) { return RecommendedDiskBytes - 1, nil },
	}, ".")
	if len(resourceChecks) != 3 {
		t.Fatalf("resource failure checks = %#v", resourceChecks)
	}
	for _, check := range resourceChecks {
		if check.OK {
			t.Fatalf("resource failure check unexpectedly passed = %#v", check)
		}
	}
}

func TestCloudflareChecksUseConnectivityOnly(t *testing.T) {
	var requested string
	checks := CloudflareChecks(context.Background(), Probes{
		HTTPStatus: func(_ context.Context, endpoint string) (int, error) {
			requested = endpoint
			return 204, nil
		},
		LookupHost: func(context.Context, string) ([]string, error) { return []string{"192.0.2.1"}, nil },
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return &probeConn{}, nil
		},
	}, []string{"edge.example.test"})
	if len(checks) != 2 || !ChecksPass(checks) {
		t.Fatalf("Cloudflare checks = %#v", checks)
	}
	if requested != CloudflareAPIURL {
		t.Fatalf("Cloudflare API endpoint = %q", requested)
	}
	if strings.Contains(requested, "token") {
		t.Fatal("Cloudflare preflight endpoint unexpectedly contains a token")
	}
}

type probeConn struct{}

func (*probeConn) Read([]byte) (int, error)           { return 0, io.EOF }
func (*probeConn) Write(contents []byte) (int, error) { return len(contents), nil }
func (*probeConn) Close() error                       { return nil }
func (*probeConn) LocalAddr() net.Addr                { return probeAddr("local") }
func (*probeConn) RemoteAddr() net.Addr               { return probeAddr("remote") }
func (*probeConn) SetDeadline(_ time.Time) error      { return nil }
func (*probeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (*probeConn) SetWriteDeadline(_ time.Time) error { return nil }

type probeAddr string

func (a probeAddr) Network() string { return "probe" }
func (a probeAddr) String() string  { return string(a) }
