package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

type runtimePurgeTestRunner struct {
	containers []string
	inspects   map[string]appRuntimePurgeInspect
	network    *appRuntimePurgeNetwork
	calls      [][]string
}

func (r *runtimePurgeTestRunner) Run(_ context.Context, _ string, _, _ io.Writer, name string, args ...string) error {
	if name != "docker" {
		return nil
	}
	r.calls = append(r.calls, append([]string(nil), args...))
	if containsArgs(args, "container", "rm") {
		id := args[len(args)-1]
		delete(r.inspects, id)
		kept := r.containers[:0]
		for _, candidate := range r.containers {
			if candidate != id {
				kept = append(kept, candidate)
			}
		}
		r.containers = kept
		if r.network != nil {
			delete(r.network.Containers, id)
		}
	}
	if containsArgs(args, "network", "rm") {
		r.network = nil
	}
	return nil
}

func (r *runtimePurgeTestRunner) Output(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	if name != "docker" {
		return nil, nil
	}
	if containsArgs(args, "container", "ls") {
		return []byte(strings.Join(r.containers, "\n")), nil
	}
	return nil, nil
}

func (r *runtimePurgeTestRunner) CombinedOutput(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	if name != "docker" {
		return nil, nil
	}
	if containsArgs(args, "network", "inspect") {
		if r.network == nil {
			return nil, errors.New("Error response from daemon: network stealth_app_runtime not found")
		}
		return json.Marshal(r.network)
	}
	if containsArgs(args, "container", "inspect") {
		id := args[len(args)-1]
		item, ok := r.inspects[id]
		if !ok {
			return nil, errors.New("Error response from daemon: no such container")
		}
		return json.Marshal([]appRuntimePurgeInspect{item})
	}
	return r.Output(context.Background(), "", name, args...)
}

func validRuntimePurgeFixture() (*runtimePurgeTestRunner, uninstallPlan) {
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	projectID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	deploymentID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	containerID := strings.Repeat("a", 64)
	container := appRuntimePurgeInspect{ID: containerID, Name: "/" + repository.AppRuntimeContainerName(appID), State: struct {
		Running bool `json:"Running"`
	}{Running: true}, HostConfig: appRuntimePurgeHostConfig{NetworkMode: "stealth_app_runtime"}}
	container.Config.Labels = map[string]string{
		"stealth.managed": "true", "stealth.resource_type": "app", "stealth.runtime_schema": "v1",
		"stealth.app_id": appID.String(), "stealth.project_id": projectID.String(),
		"stealth.deployment_id": deploymentID.String(), "stealth.generation": "3",
		"stealth.workload_spec_sha256": strings.Repeat("a", 64),
	}
	network := &appRuntimePurgeNetwork{
		Name: "stealth_app_runtime", Driver: "bridge", Scope: "local",
		Labels: map[string]string{"stealth.managed": "true", "stealth.resource_type": "app_runtime_network", "stealth.runtime_schema": "v1"},
		Containers: map[string]struct {
			Name string `json:"Name"`
		}{containerID: {Name: container.Name[1:]}},
	}
	runner := &runtimePurgeTestRunner{containers: []string{containerID}, inspects: map[string]appRuntimePurgeInspect{containerID: container}, network: network}
	return runner, uninstallPlan{appRuntimeNetwork: "stealth_app_runtime"}
}

func TestAppRuntimePurgeAcceptsOnlyExpectedHardenedNetworkPeers(t *testing.T) {
	worker := appRuntimePurgeInspect{
		Config: struct {
			Labels map[string]string `json:"Labels"`
		}{Labels: map[string]string{
			"stealth.managed": "true", "stealth.runtime_schema": appRuntimeSchema,
			"stealth.resource_type": "app_runtime_worker", "com.docker.compose.service": "worker",
		}},
		HostConfig: appRuntimePurgeHostConfig{NetworkMode: "stealth_backend"},
		Mounts: []struct {
			Type        string `json:"Type"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
		}{{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"}},
	}
	if !appRuntimePurgePeerMatches(worker) {
		t.Fatal("trusted worker with socket mount was rejected")
	}
	traefik := appRuntimePurgeInspect{
		Config: struct {
			Labels map[string]string `json:"Labels"`
		}{Labels: map[string]string{
			"stealth.managed": "true", "stealth.runtime_schema": appRuntimeSchema,
			"stealth.resource_type": "app_runtime_ingress", "com.docker.compose.service": "traefik",
		}},
		HostConfig: appRuntimePurgeHostConfig{NetworkMode: "stealth_ingress", ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}},
	}
	if !appRuntimePurgePeerMatches(traefik) {
		t.Fatal("trusted hardened Traefik peer was rejected")
	}
	worker.HostConfig.PidMode = "host"
	if appRuntimePurgePeerMatches(worker) {
		t.Fatal("worker using host PID namespace was accepted")
	}
	worker.HostConfig.PidMode = ""
	worker.HostConfig.UTSMode = "host"
	if appRuntimePurgePeerMatches(worker) {
		t.Fatal("worker using host UTS namespace was accepted")
	}
	worker.HostConfig.UTSMode = ""
	worker.HostConfig.UsernsMode = "host"
	if appRuntimePurgePeerMatches(worker) {
		t.Fatal("worker using host user namespace was accepted")
	}
	worker.HostConfig.UsernsMode = ""
	worker.HostConfig.CapAdd = []string{"NET_ADMIN"}
	if appRuntimePurgePeerMatches(worker) {
		t.Fatal("worker with added capability was accepted")
	}
	traefik.Mounts = []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	}{{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"}}
	if appRuntimePurgePeerMatches(traefik) {
		t.Fatal("Traefik with Docker socket was accepted")
	}
	traefik.Mounts = nil
	traefik.HostConfig.ReadonlyRootfs = false
	if appRuntimePurgePeerMatches(traefik) {
		t.Fatal("writable Traefik root filesystem was accepted")
	}
}

func TestAppRuntimeNetworkOwnershipInspection(t *testing.T) {
	valid := appRuntimePurgeNetwork{
		Name: "stealth_app_runtime", Driver: "bridge", Scope: "local",
		Labels: map[string]string{"stealth.managed": "true", "stealth.resource_type": "app_runtime_network", "stealth.runtime_schema": "v1"},
	}
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if !appRuntimeNetworkOwned(valid.Name, encoded) {
		t.Fatal("owned runtime bridge was rejected")
	}
	valid.Labels["stealth.managed"] = "false"
	encoded, _ = json.Marshal(valid)
	if appRuntimeNetworkOwned(valid.Name, encoded) {
		t.Fatal("foreign network label was accepted")
	}
}

func TestPurgeRefusesForeignContainerOnAppRuntimeNetwork(t *testing.T) {
	runner, plan := validRuntimePurgeFixture()
	foreignID := strings.Repeat("b", 64)
	runner.network.Containers[foreignID] = struct {
		Name string `json:"Name"`
	}{Name: "unrelated"}
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	if err := app.validateAppRuntimePurgeScope(context.Background(), plan); err == nil || !strings.Contains(err.Error(), "outside the managed App ownership set") {
		t.Fatalf("foreign runtime-network container validation error = %v", err)
	}
}

func TestPurgeRemovesOnlyValidatedAppContainersAndRuntimeNetwork(t *testing.T) {
	runner, plan := validRuntimePurgeFixture()
	app := NewApp(strings.NewReader(""), io.Discard, io.Discard)
	app.runner = runner
	if err := app.removeAppRuntimeResources(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if len(runner.containers) != 0 || runner.network != nil {
		t.Fatalf("purge left runtime resources: containers=%v network=%#v", runner.containers, runner.network)
	}
	var stopped, removed, networkRemoved bool
	for _, args := range runner.calls {
		if containsArgs(args, "container", "stop") {
			stopped = true
		}
		if containsArgs(args, "container", "rm") {
			removed = true
		}
		if containsArgs(args, "network", "rm", "stealth_app_runtime") {
			networkRemoved = true
		}
		if containsArgs(args, "system", "prune") || containsArgs(args, "container", "prune") || containsArgs(args, "network", "prune") {
			t.Fatalf("purge used broad Docker cleanup: %#v", args)
		}
	}
	if !stopped || !removed || !networkRemoved {
		t.Fatalf("purge commands missing: stop=%t rm=%t network_rm=%t calls=%#v", stopped, removed, networkRemoved, runner.calls)
	}
}
