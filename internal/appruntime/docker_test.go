package appruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

type recordedRuntimeCommand struct {
	args  []string
	stdin string
}

type scriptedRuntimeRunner struct {
	results []CommandResult
	errors  []error
	calls   []recordedRuntimeCommand
}

func (r *scriptedRuntimeRunner) Run(_ context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	var input strings.Builder
	if stdin != nil {
		_, _ = io.Copy(&input, stdin)
	}
	r.calls = append(r.calls, recordedRuntimeCommand{args: append([]string(nil), args...), stdin: input.String()})
	index := len(r.calls) - 1
	var result CommandResult
	if index < len(r.results) {
		result = r.results[index]
	}
	if index < len(r.errors) {
		return result, r.errors[index]
	}
	return result, nil
}

func runtimeTestJob() repository.AppRuntimeJob {
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	projectID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	deploymentID := uuid.MustParse("33333333-3333-4333-8333-333333333333").String()
	return repository.AppRuntimeJob{App: domain.App{
		ID: appID.String(), ProjectID: projectID.String(), Enabled: true,
		DesiredDeploymentID: &deploymentID, DesiredGeneration: 4,
		WorkloadSpecSHA256: strings.Repeat("a", 64), Workload: workloadspec.Default(),
	}}
}

func TestContainerCreateArgsKeepsTenantCommandAsArgumentsAndAppliesIsolation(t *testing.T) {
	job := runtimeTestJob()
	job.App.Workload.Command = []string{"--serve", "value; $(touch /tmp/not-run)"}
	args, err := ContainerCreateArgs(job, "stealth-app/test:runtime", "stealth_app_runtime", RuntimeSecurityProfile{})
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][]string{
		{"--name", repository.AppRuntimeContainerName(uuid.MustParse(job.App.ID))},
		{"--network", "stealth_app_runtime"},
		{"--cap-drop", "ALL"},
		{"--security-opt", "no-new-privileges:true"},
		{"--cpus", "0.5"},
		{"--memory-swap", "536870912"},
		{"--pids-limit", "256"},
		{"--restart", "no"},
	} {
		if !hasSubsequence(args, pair) {
			t.Errorf("container args are missing %q: %#v", pair, args)
		}
	}
	for _, forbidden := range []string{"-p", "--publish", "--privileged", "-v", "--volume", "--mount", "--env", "--add-host", "--network=host", "--pid=host", "--ipc=host"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("container args contain forbidden option %q: %#v", forbidden, args)
		}
	}
	if !slices.Contains(args, "value; $(touch /tmp/not-run)") {
		t.Fatalf("tenant command was not preserved as one argv item: %#v", args)
	}
	if got := args[len(args)-3:]; !slices.Equal(got, []string{"stealth-app/test:runtime", "--serve", "value; $(touch /tmp/not-run)"}) {
		t.Fatalf("image and command argv tail = %#v", got)
	}
}

func TestExecCommandRunnerPassesArgvWithoutShellExpansionAndBoundsOutput(t *testing.T) {
	printf, err := exec.LookPath("printf")
	if err != nil {
		t.Skip("printf executable is unavailable")
	}
	marker := filepath.Join(t.TempDir(), "shell-expansion")
	value := "literal; $(touch " + marker + ")"
	runner := ExecCommandRunner{DockerPath: printf, OutputLimit: 12}
	result, err := runner.Run(context.Background(), []string{"%s", value}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.StdoutTruncated || string(result.Stdout) != value[:12] {
		t.Fatalf("bounded stdout = %q truncated=%v", result.Stdout, result.StdoutTruncated)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("argv value was interpreted by a shell")
	}
}

func TestEnsureNetworkRejectsForeignNameWithoutMutation(t *testing.T) {
	foreign := NetworkInspect{Name: "stealth_app_runtime", Driver: "bridge", Scope: "local", Labels: map[string]string{"owner": "someone-else"}}
	runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: mustJSON([]NetworkInspect{foreign})}}}
	moby, err := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	err = moby.EnsureNetwork(context.Background())
	if !errors.Is(err, ErrRuntimeNetworkConflict) {
		t.Fatalf("EnsureNetwork error = %v, want ownership conflict", err)
	}
	if len(runner.calls) != 1 || !slices.Equal(runner.calls[0].args, []string{"network", "inspect", "stealth_app_runtime"}) {
		t.Fatalf("foreign network was mutated or command sequence changed: %#v", runner.calls)
	}
}

func TestEnsureNetworkCreatesOnlyTheLabeledPrivateBridge(t *testing.T) {
	network := NetworkInspect{
		Name: "stealth_app_runtime", Driver: "bridge", Scope: "local",
		Labels: map[string]string{"stealth.managed": "true", "stealth.resource_type": "app_runtime_network", "stealth.runtime_schema": "v1"},
	}
	runner := &scriptedRuntimeRunner{
		results: []CommandResult{{}, {}, {Stdout: mustJSON([]NetworkInspect{network})}},
		errors:  []error{&CommandFailure{ExitCode: 1, Stderr: "Error: No such network: stealth_app_runtime"}, nil, nil},
	}
	moby, err := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := moby.EnsureNetwork(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"network", "inspect", "stealth_app_runtime"},
		{"network", "create", "--driver", "bridge", "--label", "stealth.managed=true", "--label", "stealth.resource_type=app_runtime_network", "--label", "stealth.runtime_schema=v1", "stealth_app_runtime"},
		{"network", "inspect", "stealth_app_runtime"},
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("network command count = %d, want %d: %#v", len(runner.calls), len(want), runner.calls)
	}
	for index := range want {
		if !slices.Equal(runner.calls[index].args, want[index]) {
			t.Errorf("network command %d = %#v, want %#v", index, runner.calls[index].args, want[index])
		}
	}
}

func TestContainerCreateArgsCoverResourceBoundariesAndLiteralCommand(t *testing.T) {
	for _, test := range []struct {
		cpu    int
		memory int64
		pids   int
		want   string
	}{
		{cpu: 50, memory: 64 << 20, pids: 16, want: "0.05"},
		{cpu: 500, memory: 512 << 20, pids: 256, want: "0.5"},
		{cpu: 1000, memory: 16 << 30, pids: 2048, want: "1"},
		{cpu: 8000, memory: 512 << 20, pids: 256, want: "8"},
	} {
		job := runtimeTestJob()
		job.App.Workload.Resources.CPUMillis = test.cpu
		job.App.Workload.Resources.MemoryBytes = test.memory
		job.App.Workload.Resources.PIDsLimit = test.pids
		workingDirectory := "/srv/app directory"
		job.App.Workload.WorkingDirectory = &workingDirectory
		job.App.Workload.Command = []string{"; rm -rf /", "$(id)", "`id`", "--privileged"}
		args, err := ContainerCreateArgs(job, "stealth-app/test:runtime", "stealth_app_runtime", RuntimeSecurityProfile{})
		if err != nil {
			t.Fatalf("limits CPU=%d memory=%d pids=%d: %v", test.cpu, test.memory, test.pids, err)
		}
		for _, pair := range [][]string{
			{"--cpus", test.want},
			{"--memory", fmt.Sprint(test.memory)},
			{"--memory-swap", fmt.Sprint(test.memory)},
			{"--pids-limit", fmt.Sprint(test.pids)},
			{"--workdir", workingDirectory},
			{"--name", repository.AppRuntimeContainerName(uuid.MustParse(job.App.ID))},
			{"--network", "stealth_app_runtime"},
			{"--read-only"},
			{"--cap-drop", "ALL"},
			{"--security-opt", "no-new-privileges:true"},
			{"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=67108864"},
			{"--restart", "no"},
		} {
			if !hasSubsequence(args, pair) {
				t.Errorf("CPU=%d args missing %q: %#v", test.cpu, pair, args)
			}
		}
		imageAt := slices.Index(args, "stealth-app/test:runtime")
		if imageAt < 0 || !slices.Equal(args[imageAt:], []string{"stealth-app/test:runtime", "; rm -rf /", "$(id)", "`id`", "--privileged"}) {
			t.Errorf("tenant argv escaped image boundary: %#v", args)
		}
		for _, forbidden := range []string{"--privileged", "--device", "--publish", "--network=host", "--pid=host", "--ipc=host", "--volume", "--mount", "--volumes-from", "--runtime", "--env"} {
			if argsBeforeImageContains(args, imageAt, forbidden) {
				t.Errorf("trusted create options contain forbidden tenant-capability flag %q: %#v", forbidden, args)
			}
		}
		if argsBeforeImageContains(args, imageAt, "stealth.tenant-secret=never") {
			t.Errorf("container argv injected a tenant environment variable: %#v", args)
		}
	}
}

func TestMobyStartAndStopUseTypedDockerCommands(t *testing.T) {
	job := runtimeTestJob()
	containerID := strings.Repeat("a", 64)
	labels, err := ContainerLabels(job)
	if err != nil {
		t.Fatal(err)
	}
	running := runtimeContainerJSON(job, containerID, labels, true)
	stopped := runtimeContainerJSON(job, containerID, labels, false)
	runner := &scriptedRuntimeRunner{
		results: []CommandResult{
			{Stdout: running},
			{Stdout: running},
			{Stdout: running},
			{Stdout: running},
			{},
			{Stdout: stopped},
			{},
			{},
		},
		errors: []error{nil, nil, nil, nil, nil, nil, nil, &CommandFailure{ExitCode: 1, Stderr: "Error: No such container: " + containerID}},
	}
	moby, err := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	started, err := moby.StartApp(context.Background(), job, containerID)
	if err != nil || !started.State.Running {
		t.Fatalf("StartApp() = running=%v err=%v", started.State.Running, err)
	}
	if err := moby.RemoveApp(context.Background(), job, containerID); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(runner.calls[0].args, []string{"container", "start", containerID}) {
		t.Fatalf("start command = %#v", runner.calls[0].args)
	}
	if !slices.Equal(runner.calls[2].args, []string{"container", "inspect", containerID}) {
		t.Fatalf("remove preflight did not inspect the captured container ID: %#v", runner.calls[2].args)
	}
	if !slices.Equal(runner.calls[4].args, []string{"container", "stop", "--time", "15", containerID}) {
		t.Fatalf("stop command = %#v", runner.calls[4].args)
	}
	if !slices.Equal(runner.calls[6].args, []string{"container", "rm", containerID}) {
		t.Fatalf("remove command = %#v", runner.calls[6].args)
	}
	if len(runner.calls) != 8 {
		t.Fatalf("Docker command sequence has unexpected operations: %#v", runner.calls)
	}
}

func TestMobyRemoveAppRejectsInspectIdentityChange(t *testing.T) {
	job := runtimeTestJob()
	requestedID := strings.Repeat("a", 64)
	returnedID := strings.Repeat("b", 64)
	labels, err := ContainerLabels(job)
	if err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: runtimeContainerJSON(job, returnedID, labels, true)}}}
	moby, err := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := moby.RemoveApp(context.Background(), job, requestedID); !errors.Is(err, ErrRuntimeOwnershipConflict) {
		t.Fatalf("RemoveApp() = %v, want captured-container ownership conflict", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("RemoveApp() issued a stop/remove after inspect identity changed: %#v", runner.calls)
	}
}

func TestMobyStopGracePeriodBounds(t *testing.T) {
	for _, grace := range []int{1, 15, 120} {
		t.Run(fmt.Sprintf("%d seconds", grace), func(t *testing.T) {
			job := runtimeTestJob()
			job.App.Workload.StopGracePeriodSeconds = grace
			containerID := strings.Repeat("6", 64)
			labels, err := ContainerLabels(job)
			if err != nil {
				t.Fatal(err)
			}
			running := runtimeContainerJSON(job, containerID, labels, true)
			stopped := runtimeContainerJSON(job, containerID, labels, false)
			runner := &scriptedRuntimeRunner{
				results: []CommandResult{{Stdout: running}, {Stdout: running}, {}, {Stdout: stopped}, {}, {}},
				errors:  []error{nil, nil, nil, nil, nil, &CommandFailure{ExitCode: 1, Stderr: "Error: No such container: " + containerID}},
			}
			moby, err := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err := moby.RemoveApp(context.Background(), job, containerID); err != nil {
				t.Fatal(err)
			}
			want := []string{"container", "stop", "--time", fmt.Sprint(grace), containerID}
			if !slices.Equal(runner.calls[2].args, want) {
				t.Fatalf("stop argv = %#v, want %#v", runner.calls[2].args, want)
			}
		})
	}
}

func TestMobyCleanupRemovesOnlyFullIdentityManagedDuplicate(t *testing.T) {
	job := runtimeTestJob()
	appID := uuid.MustParse(job.App.ID)
	projectID := uuid.MustParse(job.App.ProjectID)
	containerID := strings.Repeat("7", 64)
	containerName := "/operator-renamed-duplicate"
	labels, err := ContainerLabels(job)
	if err != nil {
		t.Fatal(err)
	}
	running := runtimeContainerJSONWithName(job, containerID, labels, true, containerName)
	stopped := runtimeContainerJSONWithName(job, containerID, labels, false, containerName)
	runner := &scriptedRuntimeRunner{
		results: []CommandResult{{Stdout: running}, {Stdout: running}, {}, {Stdout: stopped}, {}, {}},
		errors:  []error{nil, nil, nil, nil, nil, &CommandFailure{ExitCode: 1, Stderr: "Error: No such container: " + containerID}},
	}
	moby, err := NewMoby(runner, "stealth_app_runtime", 30*time.Second, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := repository.AppRuntimeCleanupJob{
		ID: uuid.Must(uuid.NewV7()), ProjectID: &projectID, AppID: appID, ContainerID: &containerID,
		ContainerName: containerName, StopGracePeriodSecs: 15, WorkerID: "runtime-worker", LeaseToken: uuid.Must(uuid.NewV7()),
	}
	if err := moby.RemoveCleanupTarget(context.Background(), cleanup); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(runner.calls[2].args, []string{"container", "stop", "--time", "15", containerID}) ||
		!slices.Equal(runner.calls[4].args, []string{"container", "rm", containerID}) {
		t.Fatalf("validated duplicate cleanup command sequence = %#v", runner.calls)
	}
}

func TestEnsureImageIsIdempotentAndRepairsOnlyExpectedTag(t *testing.T) {
	configID := "sha256:" + strings.Repeat("b", 64)
	otherID := "sha256:" + strings.Repeat("c", 64)
	manifestDigest := "sha256:" + strings.Repeat("d", 64)
	info := ociartifact.ImageInfo{ManifestDigest: manifestDigest, ConfigDigest: configID, OS: "linux", Architecture: "amd64"}
	tag := "stealth-app/33333333-3333-4333-8333-333333333333:runtime"
	image := func(id string) []byte {
		return mustJSON([]map[string]any{{
			"Id": id, "Os": "linux", "Architecture": "amd64", "Variant": "",
			"RepoTags": []string{tag}, "Config": map[string]any{}, "RootFS": map[string]any{"Layers": []string{}},
		}})
	}
	t.Run("already imported with expected tag", func(t *testing.T) {
		runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: image(configID)}, {Stdout: image(configID)}}}
		moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, time.Minute)
		got, err := moby.EnsureImage(context.Background(), info, bytes.NewReader([]byte("archive")), tag)
		if err != nil || got.ID != configID || len(runner.calls) != 2 {
			t.Fatalf("idempotent import = image=%+v calls=%d err=%v", got, len(runner.calls), err)
		}
		if runner.calls[0].args[0] != "image" || runner.calls[0].args[1] != "inspect" {
			t.Fatalf("expected config digest inspection first: %#v", runner.calls)
		}
	})

	t.Run("wrong target tag is repaired without deleting images", func(t *testing.T) {
		runner := &scriptedRuntimeRunner{results: []CommandResult{{Stdout: image(configID)}, {Stdout: image(otherID)}, {}, {Stdout: image(configID)}}}
		moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, time.Minute)
		if _, err := moby.EnsureImage(context.Background(), info, bytes.NewReader([]byte("archive")), tag); err != nil {
			t.Fatal(err)
		}
		if len(runner.calls) != 4 || !slices.Equal(runner.calls[2].args, []string{"image", "tag", configID, tag}) {
			t.Fatalf("wrong tag repair sequence = %#v", runner.calls)
		}
		for _, call := range runner.calls {
			if slices.Contains(call.args, "rm") || slices.Contains(call.args, "prune") {
				t.Fatalf("tag repair deleted an image: %#v", call.args)
			}
		}
	})

	t.Run("missing image imports the verified archive once", func(t *testing.T) {
		notFound := &CommandFailure{ExitCode: 1, Stderr: "Error: No such image: " + configID}
		runner := &scriptedRuntimeRunner{
			results: []CommandResult{{}, {}, {Stdout: image(configID)}, {}, {}, {Stdout: image(configID)}},
			errors:  []error{notFound, nil, nil, notFound, nil, nil},
		}
		moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, time.Minute)
		archive := []byte("verified oci bytes")
		if _, err := moby.EnsureImage(context.Background(), info, bytes.NewReader(archive), tag); err != nil {
			t.Fatal(err)
		}
		if len(runner.calls) != 6 || !slices.Equal(runner.calls[1].args, []string{"image", "load"}) || runner.calls[1].stdin != string(archive) {
			t.Fatalf("image import sequence did not stream the archive: %#v", runner.calls)
		}
	})
}

func TestContainerInspectionRejectsMalformedOrOversizedOutput(t *testing.T) {
	for _, result := range []CommandResult{
		{Stdout: []byte("not json")},
		{Stdout: []byte("[]")},
		{Stdout: []byte("[]"), StdoutTruncated: true},
	} {
		runner := &scriptedRuntimeRunner{results: []CommandResult{result}}
		moby, _ := NewMoby(runner, "stealth_app_runtime", 30*time.Second, time.Minute)
		if _, found, err := moby.InspectApp(context.Background(), uuid.MustParse(runtimeTestJob().App.ID)); found || !errors.Is(err, ErrContainerInspection) {
			t.Fatalf("malformed Docker inspect accepted: found=%v err=%v output=%q", found, err, result.Stdout)
		}
	}
}

func TestContainerMatchesDesiredRejectsPrivilegeAndDrift(t *testing.T) {
	job := runtimeTestJob()
	image := Image{
		ID: "sha256:" + strings.Repeat("b", 64), Tag: "stealth-app/test:runtime",
		Entrypoint: []string{"/app"}, Command: []string{"serve"}, Environment: []string{"MODE=production"},
		WorkingDir: "/app", User: "10001:10001",
	}
	labels, err := ContainerLabels(job)
	if err != nil {
		t.Fatal(err)
	}
	pids := int64(256)
	initEnabled := true
	container := Container{
		ID: strings.Repeat("c", 64), Name: "/" + repository.AppRuntimeContainerName(uuid.MustParse(job.App.ID)),
		ImageID: image.ID,
		Config:  containerConfig{Labels: labels, Entrypoint: image.Entrypoint, Cmd: image.Command, Env: image.Environment, WorkingDir: image.WorkingDir, User: image.User},
		State:   containerState{Status: "running", Running: true},
		HostConfig: hostConfig{
			ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"},
			NetworkMode: "stealth_app_runtime", Memory: job.App.Workload.Resources.MemoryBytes,
			MemorySwap: job.App.Workload.Resources.MemoryBytes, NanoCpus: 500_000_000, PidsLimit: &pids,
			Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,noexec,size=67108864"},
			RestartPolicy: struct {
				Name string `json:"Name"`
			}{Name: "no"},
			LogConfig: struct {
				Type   string            `json:"Type"`
				Config map[string]string `json:"Config"`
			}{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
			Ulimits: []struct {
				Name string `json:"Name"`
				Soft int64  `json:"Soft"`
				Hard int64  `json:"Hard"`
			}{{Name: "nofile", Soft: 4096, Hard: 4096}, {Name: "core", Soft: 0, Hard: 0}},
			Init: &initEnabled,
		},
		Networks: map[string]struct{}{"stealth_app_runtime": {}},
		Mounts:   []containerMount{{Type: "tmpfs", Destination: "/tmp"}},
	}
	if !ContainerMatchesDesired(container, job, image, "stealth_app_runtime") {
		t.Fatal("complete isolated container did not match desired state")
	}
	mutations := []struct {
		name   string
		change func(*Container)
	}{
		{name: "capability added", change: func(value *Container) { value.HostConfig.CapAdd = []string{"NET_ADMIN"} }},
		{name: "host port", change: func(value *Container) {
			value.HostConfig.PortBindings = map[string][]any{"8080/tcp": {map[string]any{"HostPort": "8080"}}}
		}},
		{name: "host mount", change: func(value *Container) { value.HostConfig.Binds = []string{"/var/run/docker.sock:/var/run/docker.sock"} }},
		{name: "wrong network", change: func(value *Container) { value.Networks = map[string]struct{}{"bridge": {}} }},
		{name: "writable root", change: func(value *Container) { value.HostConfig.ReadonlyRootfs = false }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := container
			changed.Config.Labels = map[string]string{}
			for key, value := range container.Config.Labels {
				changed.Config.Labels[key] = value
			}
			changed.HostConfig = container.HostConfig
			changed.Networks = map[string]struct{}{"stealth_app_runtime": {}}
			mutation.change(&changed)
			if ContainerMatchesDesired(changed, job, image, "stealth_app_runtime") {
				t.Fatal("runtime drift was accepted as converged")
			}
		})
	}
}

func hasSubsequence(values, wanted []string) bool {
	for index := 0; index+len(wanted) <= len(values); index++ {
		if slices.Equal(values[index:index+len(wanted)], wanted) {
			return true
		}
	}
	return false
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func argsBeforeImageContains(args []string, imageAt int, value string) bool {
	if imageAt < 0 {
		return slices.Contains(args, value)
	}
	return slices.Contains(args[:imageAt], value)
}

func runtimeContainerJSON(job repository.AppRuntimeJob, id string, labels map[string]string, running bool) []byte {
	return runtimeContainerJSONWithName(job, id, labels, running, "/"+repository.AppRuntimeContainerName(uuid.MustParse(job.App.ID)))
}

func runtimeContainerJSONWithName(job repository.AppRuntimeJob, id string, labels map[string]string, running bool, name string) []byte {
	state := "created"
	if running {
		state = "running"
	}
	return mustJSON([]map[string]any{{
		"Id":              id,
		"Name":            name,
		"Config":          map[string]any{"Labels": labels},
		"State":           map[string]any{"Status": state, "Running": running},
		"NetworkSettings": map[string]any{"Networks": map[string]any{}},
	}})
}
