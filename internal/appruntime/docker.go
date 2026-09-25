package appruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

const (
	defaultRuntimeNetwork     = "stealth_app_runtime"
	defaultActionTimeout      = 30 * time.Second
	defaultImageImportTimeout = 10 * time.Minute
	defaultOutputLimit        = 1 << 20
	maxContainerList          = 10000
	runtimeSchema             = "v1"
)

var (
	ErrRuntimeUnavailable       = errors.New("Moby runtime is unavailable")
	ErrRuntimeNetworkConflict   = errors.New("App runtime network ownership conflict")
	ErrRuntimeOwnershipConflict = errors.New("App container ownership conflict")
	ErrImageVerification        = errors.New("App image verification failed")
	ErrImageImport              = errors.New("App image import failed")
	ErrInvalidRuntimeJob        = errors.New("invalid App runtime job")
	errUnsupportedImageVolumes  = errors.New("runtime image declares unsupported volumes")
	ErrContainerCreate          = errors.New("App container create failed")
	ErrContainerStart           = errors.New("App container start failed")
	ErrContainerInspection      = errors.New("App container inspection failed")
	ErrDockerObjectNotFound     = errors.New("Docker object not found")
	ErrDockerOutputTooLarge     = errors.New("Docker output exceeded its bound")
)

type RuntimeSecurityProfile struct {
	// Runtime is platform-owned. Empty means Docker's configured default.
	Runtime string
}

type CommandResult struct {
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
}

// CommandRunner executes one typed Docker argv vector and can stream an image
// archive to stdin. Implementations must not invoke a shell.
type CommandRunner interface {
	Run(context.Context, []string, io.Reader) (CommandResult, error)
}

type ExecCommandRunner struct {
	DockerPath  string
	OutputLimit int
}

type CommandFailure struct {
	ExitCode int
	Stderr   string
}

func (e *CommandFailure) Error() string { return "Docker command failed" }

type boundedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		b.truncated = b.truncated || original > 0
		return original, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		b.truncated = true
	}
	b.data = append(b.data, value...)
	return original, nil
}

func (b *boundedBuffer) Bytes() []byte { return b.data }

func (r ExecCommandRunner) Run(ctx context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	path := strings.TrimSpace(r.DockerPath)
	if path == "" {
		path = "docker"
	}
	limit := r.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Stdin = stdin
	command.Env = []string{"HOME=/tmp"}
	if pathValue := os.Getenv("PATH"); pathValue != "" {
		command.Env = append(command.Env, "PATH="+pathValue)
	}
	stdout := &boundedBuffer{limit: limit}
	stderr := &boundedBuffer{limit: 32 << 10}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return result, &CommandFailure{ExitCode: exitError.ExitCode(), Stderr: string(result.Stderr)}
	}
	return result, err
}

type Moby struct {
	Runner        CommandRunner
	NetworkName   string
	ActionTimeout time.Duration
	ImportTimeout time.Duration
	Security      RuntimeSecurityProfile
}

func NewMoby(runner CommandRunner, networkName string, actionTimeout, importTimeout time.Duration) (*Moby, error) {
	if runner == nil {
		runner = ExecCommandRunner{}
	}
	if networkName == "" {
		networkName = defaultRuntimeNetwork
	}
	if !validDockerName(networkName) {
		return nil, errors.New("invalid App runtime network name")
	}
	if actionTimeout <= 0 {
		actionTimeout = defaultActionTimeout
	}
	if actionTimeout < 5*time.Second || actionTimeout > 2*time.Minute {
		return nil, errors.New("App runtime action timeout is outside its supported range")
	}
	if importTimeout <= 0 {
		importTimeout = defaultImageImportTimeout
	}
	if importTimeout < time.Minute || importTimeout > 30*time.Minute {
		return nil, errors.New("App image import timeout is outside its supported range")
	}
	return &Moby{Runner: runner, NetworkName: networkName, ActionTimeout: actionTimeout, ImportTimeout: importTimeout}, nil
}

type NetworkInspect struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Driver   string            `json:"Driver"`
	Scope    string            `json:"Scope"`
	Internal bool              `json:"Internal"`
	Labels   map[string]string `json:"Labels"`
	IPAM     struct {
		Config []struct {
			Subnet string `json:"Subnet"`
		} `json:"Config"`
	} `json:"IPAM"`
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
}

type Image struct {
	ID           string
	Tag          string
	OS           string
	Architecture string
	Variant      string
	Layers       []string
	Entrypoint   []string
	Command      []string
	Environment  []string
	WorkingDir   string
	User         string
	VolumePaths  []string
}

type Container struct {
	ID         string                      `json:"Id"`
	Name       string                      `json:"Name"`
	ImageID    string                      `json:"Image"`
	Config     containerConfig             `json:"Config"`
	State      containerState              `json:"State"`
	HostConfig hostConfig                  `json:"HostConfig"`
	Networks   map[string]ContainerNetwork `json:"-"`
	Mounts     []containerMount            `json:"Mounts"`
}

type ContainerNetwork struct {
	NetworkID string `json:"NetworkID"`
	IPAddress string `json:"IPAddress"`
}

type containerConfig struct {
	Labels     map[string]string `json:"Labels"`
	Cmd        []string          `json:"Cmd"`
	Entrypoint []string          `json:"Entrypoint"`
	Env        []string          `json:"Env"`
	WorkingDir string            `json:"WorkingDir"`
	User       string            `json:"User"`
}

type containerState struct {
	Status    string `json:"Status"`
	Running   bool   `json:"Running"`
	ExitCode  int    `json:"ExitCode"`
	OOMKilled bool   `json:"OOMKilled"`
}

type hostConfig struct {
	Privileged     bool              `json:"Privileged"`
	ReadonlyRootfs bool              `json:"ReadonlyRootfs"`
	AutoRemove     bool              `json:"AutoRemove"`
	CapAdd         []string          `json:"CapAdd"`
	CapDrop        []string          `json:"CapDrop"`
	SecurityOpt    []string          `json:"SecurityOpt"`
	NetworkMode    string            `json:"NetworkMode"`
	Memory         int64             `json:"Memory"`
	MemorySwap     int64             `json:"MemorySwap"`
	NanoCpus       int64             `json:"NanoCpus"`
	PidsLimit      *int64            `json:"PidsLimit"`
	Tmpfs          map[string]string `json:"Tmpfs"`
	Binds          []string          `json:"Binds"`
	VolumesFrom    []string          `json:"VolumesFrom"`
	PortBindings   map[string][]any  `json:"PortBindings"`
	Devices        []any             `json:"Devices"`
	PidMode        string            `json:"PidMode"`
	IpcMode        string            `json:"IpcMode"`
	UTSMode        string            `json:"UTSMode"`
	UsernsMode     string            `json:"UsernsMode"`
	RestartPolicy  struct {
		Name string `json:"Name"`
	} `json:"RestartPolicy"`
	LogConfig struct {
		Type   string            `json:"Type"`
		Config map[string]string `json:"Config"`
	} `json:"LogConfig"`
	Ulimits []struct {
		Name string `json:"Name"`
		Soft int64  `json:"Soft"`
		Hard int64  `json:"Hard"`
	} `json:"Ulimits"`
	Init *bool `json:"Init"`
}

type containerMount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

type imageInspect struct {
	ID           string   `json:"Id"`
	OS           string   `json:"Os"`
	Architecture string   `json:"Architecture"`
	Variant      string   `json:"Variant"`
	RepoTags     []string `json:"RepoTags"`
	Config       struct {
		Entrypoint []string            `json:"Entrypoint"`
		Cmd        []string            `json:"Cmd"`
		Env        []string            `json:"Env"`
		WorkingDir string              `json:"WorkingDir"`
		User       string              `json:"User"`
		Volumes    map[string]struct{} `json:"Volumes"`
	} `json:"Config"`
	RootFS struct {
		Layers []string `json:"Layers"`
	} `json:"RootFS"`
}

// EnsureNetwork creates only the separately labeled bridge. Existing names
// are reused only when the inspected driver and ownership labels match.
func (m *Moby) EnsureNetwork(ctx context.Context) error {
	network, found, err := m.inspectNetwork(ctx)
	if err != nil && !errors.Is(err, ErrDockerObjectNotFound) {
		return err
	}
	if found {
		return m.validateNetwork(ctx, network)
	}
	args := []string{"network", "create", "--driver", "bridge"}
	labels := networkLabels()
	for _, key := range []string{"stealth.managed", "stealth.resource_type", "stealth.runtime_schema"} {
		args = append(args, "--label", key+"="+labels[key])
	}
	args = append(args, m.NetworkName)
	_, createErr := m.runAction(ctx, args, nil)
	// A second worker may win creation, or the create response may be lost
	// after Docker made the network. Inspect the result before classifying it.
	network, found, err = m.inspectNetwork(ctx)
	if err != nil {
		return err
	}
	if !found {
		if createErr != nil {
			return createErr
		}
		return ErrRuntimeNetworkConflict
	}
	if err := m.validateNetwork(ctx, network); err != nil {
		return err
	}
	return nil
}

// EnsureRuntimeNetworkPeers validates the owned runtime bridge and joins only
// trusted Compose worker and Traefik peers. It is retried periodically so a
// Compose recreation or Docker daemon restart repairs membership.
func (m *Moby) EnsureRuntimeNetworkPeers(ctx context.Context) error {
	network, found, err := m.inspectNetwork(ctx)
	if err != nil {
		return err
	}
	if !found {
		return ErrRuntimeNetworkConflict
	}
	if err := m.validateNetwork(ctx, network); err != nil {
		return err
	}
	return m.ensureRuntimeNetworkPeers(ctx)
}

func (m *Moby) inspectNetwork(ctx context.Context) (NetworkInspect, bool, error) {
	result, err := m.runAction(ctx, []string{"network", "inspect", m.NetworkName}, nil)
	if errors.Is(err, ErrDockerObjectNotFound) {
		return NetworkInspect{}, false, nil
	}
	if err != nil {
		return NetworkInspect{}, false, err
	}
	var values []NetworkInspect
	if result.StdoutTruncated || json.Unmarshal(result.Stdout, &values) != nil || len(values) != 1 {
		return NetworkInspect{}, false, ErrRuntimeNetworkConflict
	}
	return values[0], true, nil
}

func (m *Moby) validateNetwork(ctx context.Context, network NetworkInspect) error {
	labels := networkLabels()
	if network.Name != m.NetworkName || network.Driver != "bridge" || network.Scope != "local" || network.Internal || !hasLabels(network.Labels, labels) {
		return ErrRuntimeNetworkConflict
	}
	for containerID := range network.Containers {
		if !validRuntimeID(containerID) {
			return ErrRuntimeNetworkConflict
		}
		container, found, err := m.inspectContainer(ctx, containerID)
		if err != nil {
			return err
		}
		if !found || (!managedAppContainer(container) && !managedRuntimeNetworkPeer(container)) {
			return ErrRuntimeNetworkConflict
		}
	}
	return nil
}

// ensureRuntimeNetworkPeers joins only the current trusted worker and
// Traefik containers to the separately owned App bridge. Apps remain isolated
// from all backend networks and no host ports are published.
func (m *Moby) ensureRuntimeNetworkPeers(ctx context.Context) error {
	for _, peer := range []struct {
		resourceType string
		service      string
		optional     bool
	}{
		{resourceType: "app_runtime_worker", service: "worker"},
		{resourceType: "app_runtime_ingress", service: "traefik", optional: true},
	} {
		result, err := m.runAction(ctx, []string{
			"container", "ls", "--quiet", "--no-trunc",
			"--filter", "label=stealth.managed=true",
			"--filter", "label=stealth.resource_type=" + peer.resourceType,
			"--filter", "label=stealth.runtime_schema=" + runtimeSchema,
		}, nil)
		if err != nil || result.StdoutTruncated {
			return errors.Join(ErrRuntimeNetworkConflict, err)
		}
		ids := strings.Fields(string(result.Stdout))
		if len(ids) == 0 && peer.optional {
			continue
		}
		if len(ids) != 1 || !validRuntimeID(ids[0]) {
			return ErrRuntimeNetworkConflict
		}
		container, found, err := m.inspectContainer(ctx, ids[0])
		if err != nil || !found || !runtimeNetworkPeerMatches(container, peer.resourceType, peer.service) {
			return errors.Join(ErrRuntimeNetworkConflict, err)
		}
		network, found, err := m.inspectNetwork(ctx)
		if err != nil || !found || m.validateNetwork(ctx, network) != nil {
			return errors.Join(ErrRuntimeNetworkConflict, err)
		}
		if _, connected := network.Containers[container.ID]; connected {
			continue
		}
		_, connectErr := m.runAction(ctx, []string{"network", "connect", m.NetworkName, container.ID}, nil)
		// A lost response or another worker may have completed the connection.
		network, found, err = m.inspectNetwork(ctx)
		if err != nil || !found || m.validateNetwork(ctx, network) != nil {
			return errors.Join(ErrRuntimeNetworkConflict, connectErr, err)
		}
		if _, connected := network.Containers[container.ID]; !connected {
			return errors.Join(ErrRuntimeNetworkConflict, connectErr)
		}
	}
	return nil
}

func managedRuntimeNetworkPeer(container Container) bool {
	labels := container.Config.Labels
	return runtimeNetworkPeerMatches(container, labels["stealth.resource_type"], labels["com.docker.compose.service"])
}

func runtimeNetworkPeerMatches(container Container, resourceType, service string) bool {
	labels := container.Config.Labels
	if labels["stealth.managed"] != "true" || labels["stealth.runtime_schema"] != runtimeSchema ||
		labels["stealth.resource_type"] != resourceType || labels["com.docker.compose.service"] != service ||
		container.HostConfig.Privileged || container.HostConfig.NetworkMode == "host" || container.HostConfig.NetworkMode == "none" || container.HostConfig.PidMode == "host" || container.HostConfig.IpcMode == "host" || container.HostConfig.UTSMode == "host" || container.HostConfig.UsernsMode == "host" ||
		len(container.HostConfig.PortBindings) != 0 || len(container.HostConfig.CapAdd) != 0 {
		return false
	}
	switch resourceType {
	case "app_runtime_worker":
		return service == "worker" && hasDockerSocketMount(container.Mounts)
	case "app_runtime_ingress":
		return service == "traefik" && container.HostConfig.ReadonlyRootfs && slices.Contains(container.HostConfig.CapDrop, "ALL") &&
			slices.Contains(container.HostConfig.SecurityOpt, "no-new-privileges:true") && !hasMountDestination(container.Mounts, "/var/run/docker.sock")
	default:
		return false
	}
}

func hasMountDestination(mounts []containerMount, destination string) bool {
	for _, mount := range mounts {
		if filepath.Clean(mount.Destination) == destination {
			return true
		}
	}
	return false
}

func hasDockerSocketMount(mounts []containerMount) bool {
	for _, mount := range mounts {
		if mount.Type == "bind" && filepath.Clean(mount.Source) == "/var/run/docker.sock" && filepath.Clean(mount.Destination) == "/var/run/docker.sock" {
			return true
		}
	}
	return false
}

func networkLabels() map[string]string {
	return map[string]string{
		"stealth.managed":        "true",
		"stealth.resource_type":  "app_runtime_network",
		"stealth.runtime_schema": runtimeSchema,
	}
}

// EnsureImage verifies the selected OCI manifest and config identity before
// creating a deterministic Moby tag. The Docker image ID is the OCI config
// digest; it is distinct from AppDeployment.image_digest (manifest digest).
func (m *Moby) EnsureImage(ctx context.Context, info ociartifact.ImageInfo, archive io.ReadSeeker, runtimeTag string) (Image, error) {
	if archive == nil || !validImageTag(runtimeTag) || info.ManifestDigest == "" || info.ConfigDigest == "" || len(info.VolumePaths) != 0 {
		if len(info.VolumePaths) != 0 {
			return Image{}, ErrImageVerification
		}
		return Image{}, ErrImageVerification
	}
	image, found, err := m.inspectImage(ctx, info.ConfigDigest)
	if err != nil && !errors.Is(err, ErrDockerObjectNotFound) {
		return Image{}, err
	}
	if !found {
		if _, err := archive.Seek(0, io.SeekStart); err != nil {
			return Image{}, ErrImageVerification
		}
		importContext, cancel := context.WithTimeout(ctx, m.ImportTimeout)
		archiveReader, archiveWriter := io.Pipe()
		conversionDone := make(chan error, 1)
		go func() {
			conversionErr := ociartifact.WriteDockerArchive(archive, info, archiveWriter)
			_ = archiveWriter.CloseWithError(conversionErr)
			conversionDone <- conversionErr
		}()
		_, importErr := m.run(importContext, []string{"image", "load"}, archiveReader)
		_ = archiveReader.Close()
		if conversionErr := <-conversionDone; conversionErr != nil {
			importErr = errors.Join(importErr, conversionErr)
		}
		cancel()
		image, found, err = m.inspectImage(ctx, info.ConfigDigest)
		if err != nil && !errors.Is(err, ErrDockerObjectNotFound) {
			return Image{}, err
		}
		if !found {
			if importErr != nil {
				return Image{}, errors.Join(ErrImageImport, importErr)
			}
			return Image{}, ErrImageImport
		}
	}
	if !imageMatchesOCI(image, info) {
		return Image{}, ErrImageVerification
	}
	image.Tag = runtimeTag
	tagged, found, err := m.inspectImage(ctx, runtimeTag)
	if err != nil && !errors.Is(err, ErrDockerObjectNotFound) {
		return Image{}, err
	}
	if !found || tagged.ID != image.ID {
		if _, err := m.runAction(ctx, []string{"image", "tag", image.ID, runtimeTag}, nil); err != nil {
			return Image{}, errors.Join(ErrImageImport, err)
		}
		tagged, found, err = m.inspectImage(ctx, runtimeTag)
		if err != nil || !found || tagged.ID != image.ID {
			return Image{}, ErrImageVerification
		}
	}
	return image, nil
}

func (m *Moby) inspectImage(ctx context.Context, reference string) (Image, bool, error) {
	result, err := m.runAction(ctx, []string{"image", "inspect", reference}, nil)
	if errors.Is(err, ErrDockerObjectNotFound) {
		return Image{}, false, nil
	}
	if err != nil {
		return Image{}, false, err
	}
	var values []imageInspect
	if result.StdoutTruncated || json.Unmarshal(result.Stdout, &values) != nil || len(values) != 1 {
		return Image{}, false, ErrImageVerification
	}
	item := values[0]
	image := Image{
		ID: item.ID, OS: item.OS, Architecture: item.Architecture, Variant: item.Variant,
		Layers:      append([]string(nil), item.RootFS.Layers...),
		Entrypoint:  append([]string(nil), item.Config.Entrypoint...),
		Command:     append([]string(nil), item.Config.Cmd...),
		Environment: append([]string(nil), item.Config.Env...),
		WorkingDir:  item.Config.WorkingDir, User: item.Config.User,
	}
	for volume := range item.Config.Volumes {
		image.VolumePaths = append(image.VolumePaths, volume)
	}
	slices.Sort(image.VolumePaths)
	if !validImageID(image.ID) {
		return Image{}, false, ErrImageVerification
	}
	return image, true, nil
}

func imageMatchesOCI(image Image, info ociartifact.ImageInfo) bool {
	return image.ID == info.ConfigDigest && image.OS == info.OS && image.Architecture == info.Architecture &&
		image.Variant == info.Variant && slices.Equal(image.Layers, info.LayerDiffIDs) && len(image.VolumePaths) == 0
}

func ImageTag(deploymentID uuid.UUID) string {
	return "stealth-app/" + deploymentID.String() + ":runtime"
}

func (m *Moby) InspectApp(ctx context.Context, appID uuid.UUID) (Container, bool, error) {
	if appID == uuid.Nil {
		return Container{}, false, ErrContainerInspection
	}
	result, err := m.runAction(ctx, []string{
		"container", "ls", "--all", "--quiet", "--no-trunc",
		"--filter", "label=stealth.managed=true",
		"--filter", "label=stealth.resource_type=app",
		"--filter", "label=stealth.app_id=" + appID.String(),
	}, nil)
	if err != nil {
		return Container{}, false, err
	}
	if result.StdoutTruncated {
		return Container{}, false, ErrDockerOutputTooLarge
	}
	ids := strings.Fields(string(result.Stdout))
	if len(ids) > 1 {
		return Container{}, false, ErrRuntimeOwnershipConflict
	}
	if len(ids) == 0 {
		return Container{}, false, nil
	}
	if !validRuntimeID(ids[0]) {
		return Container{}, false, ErrContainerInspection
	}
	return m.inspectContainer(ctx, ids[0])
}

// ProbeApp probes only the current owned container on the Stealth App bridge.
// The destination is rebuilt from that container's Docker inspection and
// checked against the owned network subnet for every probe.
func (m *Moby) ProbeApp(ctx context.Context, job repository.AppHealthCheckJob, expected Container) error {
	if !validHealthCheckJobIdentity(job) || !containerMatchesHealthIdentity(expected, job, m.NetworkName) {
		return ErrHealthRuntimeDrift
	}
	latest, found, err := m.inspectContainer(ctx, expected.ID)
	if err != nil || !found || latest.ID != expected.ID || !containerMatchesHealthIdentity(latest, job, m.NetworkName) {
		return errors.Join(ErrHealthRuntimeDrift, err)
	}
	network, found, err := m.inspectNetwork(ctx)
	if err != nil || !found || m.validateNetwork(ctx, network) != nil {
		return errors.Join(ErrHealthRuntimeDrift, err)
	}
	address, ok := managedContainerAddress(latest, network, m.NetworkName)
	if !ok || address != job.Address {
		return ErrHealthRuntimeDrift
	}
	return runHealthProbe(ctx, address, job.App.Workload)
}

func managedContainerAddress(container Container, network NetworkInspect, networkName string) (string, bool) {
	attachment, attached := container.Networks[networkName]
	if !attached || attachment.IPAddress == "" || network.ID == "" || attachment.NetworkID != network.ID {
		return "", false
	}
	address := net.ParseIP(attachment.IPAddress)
	if address == nil || address.To4() == nil || !address.IsPrivate() || address.IsLoopback() || address.IsUnspecified() || address.IsLinkLocalUnicast() || address.IsMulticast() {
		return "", false
	}
	for _, configured := range network.IPAM.Config {
		_, subnet, err := net.ParseCIDR(configured.Subnet)
		if err == nil && subnet.Contains(address) {
			return address.To4().String(), true
		}
	}
	return "", false
}

func containerAddress(container Container, networkName string) string {
	return container.Networks[networkName].IPAddress
}

func validHealthCheckJobIdentity(job repository.AppHealthCheckJob) bool {
	if job.App.DesiredDeploymentID == nil || job.App.WorkloadSpecSHA256 == "" {
		return false
	}
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	deploymentID, deploymentErr := uuid.Parse(*job.App.DesiredDeploymentID)
	workload, workloadErr := workloadspec.Normalize(job.App.Workload)
	workloadDigest, digestErr := workloadspec.Digest(workload)
	return appErr == nil && projectErr == nil && deploymentErr == nil && appID != uuid.Nil && projectID != uuid.Nil && deploymentID != uuid.Nil &&
		job.RouteIdentity != uuid.Nil && job.ContainerName == repository.AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) &&
		job.App.Enabled && job.App.RuntimeStatus == "running" && job.App.ObservedGeneration == job.App.DesiredGeneration &&
		job.App.DesiredGeneration > 0 && validRuntimeID(job.ContainerID) && job.LeaseToken != uuid.Nil && job.WorkerID != "" &&
		len(job.App.WorkloadSpecSHA256) == 64 && workloadErr == nil && digestErr == nil && workloadDigest == job.App.WorkloadSpecSHA256 &&
		validPrivateProbeAddress(job.Address)
}

func containerMatchesHealthIdentity(container Container, job repository.AppHealthCheckJob, networkName string) bool {
	if !validHealthCheckJobIdentity(job) || job.App.DesiredDeploymentID == nil {
		return false
	}
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	if appErr != nil || projectErr != nil || !managedForApp(container, appID, projectID) ||
		container.Name != "/"+job.ContainerName || container.ID != job.ContainerID || !container.State.Running {
		return false
	}
	deploymentID, err := uuid.Parse(*job.App.DesiredDeploymentID)
	if err != nil {
		return false
	}
	expected := map[string]string{
		"stealth.managed": "true", "stealth.resource_type": "app", "stealth.app_id": appID.String(),
		"stealth.project_id": projectID.String(), "stealth.deployment_id": deploymentID.String(),
		"stealth.generation":           strconv.FormatInt(job.App.DesiredGeneration, 10),
		"stealth.workload_spec_sha256": job.App.WorkloadSpecSHA256, "stealth.runtime_schema": runtimeSchema,
	}
	if !hasLabels(container.Config.Labels, expected) || container.HostConfig.NetworkMode != networkName || len(container.Networks) != 1 ||
		container.HostConfig.Privileged || container.HostConfig.AutoRemove || len(container.HostConfig.CapAdd) != 0 || !container.HostConfig.ReadonlyRootfs ||
		!slices.Contains(container.HostConfig.CapDrop, "ALL") || !slices.Contains(container.HostConfig.SecurityOpt, "no-new-privileges:true") ||
		container.HostConfig.Memory != job.App.Workload.Resources.MemoryBytes || container.HostConfig.MemorySwap != job.App.Workload.Resources.MemoryBytes ||
		container.HostConfig.NanoCpus != int64(job.App.Workload.Resources.CPUMillis)*1_000_000 || container.HostConfig.PidsLimit == nil || *container.HostConfig.PidsLimit != int64(job.App.Workload.Resources.PIDsLimit) ||
		container.HostConfig.NetworkMode == "host" || container.HostConfig.PidMode == "host" || container.HostConfig.IpcMode == "host" ||
		container.HostConfig.UTSMode == "host" || container.HostConfig.UsernsMode == "host" || len(container.HostConfig.Binds) != 0 ||
		len(container.HostConfig.VolumesFrom) != 0 || len(container.HostConfig.PortBindings) != 0 || len(container.HostConfig.Devices) != 0 ||
		!exactTmpfs(container.HostConfig.Tmpfs) || !noUnexpectedMounts(container.Mounts) {
		return false
	}
	_, ok := container.Networks[networkName]
	return ok
}

func (m *Moby) inspectContainer(ctx context.Context, identifier string) (Container, bool, error) {
	result, err := m.runAction(ctx, []string{"container", "inspect", identifier}, nil)
	if errors.Is(err, ErrDockerObjectNotFound) {
		return Container{}, false, nil
	}
	if err != nil {
		return Container{}, false, err
	}
	var values []struct {
		Container
		NetworkSettings struct {
			Networks map[string]ContainerNetwork `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if result.StdoutTruncated || json.Unmarshal(result.Stdout, &values) != nil || len(values) != 1 {
		return Container{}, false, ErrContainerInspection
	}
	container := values[0].Container
	if container.ID == "" || !validRuntimeID(container.ID) || container.Name == "" {
		return Container{}, false, ErrContainerInspection
	}
	container.Networks = values[0].NetworkSettings.Networks
	if container.Networks == nil {
		container.Networks = map[string]ContainerNetwork{}
	}
	return container, true, nil
}

func (m *Moby) CreateApp(ctx context.Context, job repository.AppRuntimeJob, image Image) (Container, error) {
	args, err := ContainerCreateArgs(job, image.Tag, m.NetworkName, m.Security)
	if err != nil {
		return Container{}, err
	}
	_, createErr := m.runAction(ctx, args, nil)
	container, found, inspectErr := m.InspectApp(ctx, uuid.MustParse(job.App.ID))
	if inspectErr != nil {
		return Container{}, inspectErr
	}
	if !found {
		if createErr != nil {
			return Container{}, errors.Join(ErrContainerCreate, createErr)
		}
		return Container{}, ErrContainerCreate
	}
	return container, nil
}

// RenameApp rotates the container's Docker DNS identity before a restarted
// process starts. Docker updates the endpoint's DNS names with the rename, so
// an old Traefik snapshot cannot resolve its old target to the new process.
func (m *Moby) RenameApp(ctx context.Context, job repository.AppRuntimeJob, containerID, targetName string) (Container, error) {
	appID, appErr := uuid.Parse(job.App.ID)
	projectID, projectErr := uuid.Parse(job.App.ProjectID)
	if appErr != nil || projectErr != nil || appID == uuid.Nil || projectID == uuid.Nil ||
		!validRuntimeID(containerID) || targetName != job.ContainerName ||
		targetName != repository.AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) {
		return Container{}, ErrRuntimeOwnershipConflict
	}
	container, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found {
		if err != nil {
			return Container{}, err
		}
		return Container{}, ErrRuntimeOwnershipConflict
	}
	if container.ID != containerID || !managedForApp(container, appID, projectID) || !repository.ValidAppRuntimeContainerName(appID, targetName) {
		return Container{}, ErrRuntimeOwnershipConflict
	}
	if strings.TrimPrefix(container.Name, "/") != targetName {
		if _, err := m.runAction(ctx, []string{"container", "rename", container.ID, targetName}, nil); err != nil {
			return Container{}, errors.Join(ErrRuntimeOwnershipConflict, err)
		}
	}
	renamed, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found || renamed.ID != containerID || renamed.Name != "/"+targetName || !managedForApp(renamed, appID, projectID) {
		if err != nil {
			return Container{}, err
		}
		return Container{}, ErrRuntimeOwnershipConflict
	}
	return renamed, nil
}

func (m *Moby) StartApp(ctx context.Context, job repository.AppRuntimeJob, containerID string) (Container, error) {
	if !validRuntimeID(containerID) {
		return Container{}, ErrContainerStart
	}
	_, startErr := m.runAction(ctx, []string{"container", "start", containerID}, nil)
	container, found, inspectErr := m.InspectApp(ctx, uuid.MustParse(job.App.ID))
	if inspectErr != nil {
		return Container{}, inspectErr
	}
	if !found || container.ID != containerID || !container.State.Running {
		if startErr != nil {
			return Container{}, errors.Join(ErrContainerStart, startErr)
		}
		return Container{}, ErrContainerStart
	}
	return container, nil
}

func (m *Moby) RemoveApp(ctx context.Context, job repository.AppRuntimeJob, containerID string) error {
	if !validRuntimeID(containerID) {
		return ErrRuntimeOwnershipConflict
	}
	appID := uuid.MustParse(job.App.ID)
	container, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found {
		return err
	}
	if container.ID != containerID || !managedForApp(container, appID, uuid.MustParse(job.App.ProjectID)) {
		return ErrRuntimeOwnershipConflict
	}
	return m.stopAndRemove(ctx, containerID, job.App.Workload.StopGracePeriodSeconds)
}

func (m *Moby) RemoveCleanupTarget(ctx context.Context, job repository.AppRuntimeCleanupJob) error {
	identifier := job.ContainerName
	if job.ContainerID != nil {
		identifier = *job.ContainerID
	}
	container, found, err := m.inspectContainer(ctx, identifier)
	if err != nil || !found {
		return err
	}
	ownerAppID, ownerProjectID, validOwner := validManagedAppContainerIdentity(container)
	if !validOwner || ownerAppID != job.AppID {
		return ErrRuntimeOwnershipConflict
	}
	if job.ProjectID != nil && ownerProjectID != *job.ProjectID {
		return ErrRuntimeOwnershipConflict
	}
	if job.ContainerID != nil && container.ID != *job.ContainerID {
		return ErrRuntimeOwnershipConflict
	}
	storedName := strings.TrimPrefix(job.ContainerName, "/")
	actualName := strings.TrimPrefix(container.Name, "/")
	if actualName != storedName && (!repository.ValidAppRuntimeContainerName(job.AppID, actualName) || !repository.ValidAppRuntimeContainerName(job.AppID, storedName)) {
		return ErrRuntimeOwnershipConflict
	}
	if job.ContainerID == nil && !validRuntimeNameForApp(job.AppID, storedName) {
		return ErrRuntimeOwnershipConflict
	}
	return m.stopAndRemove(ctx, container.ID, job.StopGracePeriodSecs)
}

func (m *Moby) stopAndRemove(ctx context.Context, containerID string, grace int) error {
	if !validRuntimeID(containerID) || grace < 1 || grace > 120 {
		return ErrRuntimeOwnershipConflict
	}
	container, found, err := m.inspectContainer(ctx, containerID)
	if err != nil || !found {
		return err
	}
	if container.ID != containerID {
		return ErrRuntimeOwnershipConflict
	}
	if container.State.Running {
		_, stopErr := m.runStopAction(ctx, []string{"container", "stop", "--time", strconv.Itoa(grace), container.ID}, grace)
		stopped, stillFound, inspectErr := m.inspectContainer(ctx, container.ID)
		if inspectErr != nil {
			return inspectErr
		}
		if stillFound && stopped.State.Running {
			if stopErr != nil {
				return errors.Join(ErrRuntimeUnavailable, stopErr)
			}
			return ErrRuntimeUnavailable
		}
		if !stillFound {
			return nil
		}
	}
	_, removeErr := m.runAction(ctx, []string{"container", "rm", containerID}, nil)
	remaining, found, inspectErr := m.inspectContainer(ctx, containerID)
	if inspectErr != nil {
		return inspectErr
	}
	if found {
		_ = remaining
		if removeErr != nil {
			return errors.Join(ErrRuntimeUnavailable, removeErr)
		}
		return ErrRuntimeUnavailable
	}
	return nil
}

func (m *Moby) ListManagedAppContainers(ctx context.Context) ([]Container, error) {
	result, err := m.runAction(ctx, []string{
		"container", "ls", "--all", "--quiet", "--no-trunc",
		"--filter", "label=stealth.managed=true",
		"--filter", "label=stealth.resource_type=app",
	}, nil)
	if err != nil {
		return nil, err
	}
	if result.StdoutTruncated {
		return nil, ErrDockerOutputTooLarge
	}
	lines := strings.Fields(string(result.Stdout))
	if len(lines) > maxContainerList {
		return nil, ErrDockerOutputTooLarge
	}
	containers := make([]Container, 0, len(lines))
	for _, id := range lines {
		if !validRuntimeID(id) {
			return nil, ErrContainerInspection
		}
		container, found, err := m.inspectContainer(ctx, id)
		if err != nil {
			return nil, err
		}
		if found {
			containers = append(containers, container)
		}
	}
	return containers, nil
}

func (m *Moby) runAction(parent context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	return m.runActionWithTimeout(parent, m.ActionTimeout, args, stdin)
}

func (m *Moby) runStopAction(parent context.Context, args []string, grace int) (CommandResult, error) {
	timeout := m.ActionTimeout
	if grace > 0 && time.Duration(grace)*time.Second > timeout {
		timeout = time.Duration(grace) * time.Second
	}
	return m.runActionWithTimeout(parent, timeout, args, nil)
}

func (m *Moby) runActionWithTimeout(parent context.Context, timeout time.Duration, args []string, stdin io.Reader) (CommandResult, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return m.run(ctx, args, stdin)
}

func (m *Moby) run(ctx context.Context, args []string, stdin io.Reader) (CommandResult, error) {
	result, err := m.Runner.Run(ctx, args, stdin)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if dockerNotFound(err) {
			return result, ErrDockerObjectNotFound
		}
		return result, errors.Join(ErrRuntimeUnavailable, err)
	}
	return result, nil
}

func dockerNotFound(err error) bool {
	var failure *CommandFailure
	if errors.As(err, &failure) {
		message := strings.ToLower(failure.Stderr)
		return strings.Contains(message, "no such container") || strings.Contains(message, "no such image") ||
			strings.Contains(message, "no such network") ||
			(strings.Contains(message, "network ") && strings.Contains(message, " not found")) ||
			strings.Contains(message, "no such object")
	}
	return false
}

func ContainerCreateArgs(job repository.AppRuntimeJob, imageRef, networkName string, profile RuntimeSecurityProfile) ([]string, error) {
	appID, err := uuid.Parse(job.App.ID)
	if err != nil || appID == uuid.Nil {
		return nil, ErrContainerCreate
	}
	projectID, err := uuid.Parse(job.App.ProjectID)
	if err != nil || projectID == uuid.Nil || job.App.DesiredDeploymentID == nil || job.App.WorkloadSpecSHA256 == "" || !validDockerName(networkName) || !validImageTag(imageRef) {
		return nil, ErrContainerCreate
	}
	if job.RouteIdentity == uuid.Nil || job.ContainerName != repository.AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity) {
		return nil, ErrContainerCreate
	}
	if profile.Runtime != "" && !validDockerName(profile.Runtime) {
		return nil, ErrContainerCreate
	}
	spec, err := workloadspec.Normalize(job.App.Workload)
	if err != nil {
		return nil, ErrContainerCreate
	}
	labels := map[string]string{
		"stealth.managed":              "true",
		"stealth.resource_type":        "app",
		"stealth.app_id":               appID.String(),
		"stealth.project_id":           projectID.String(),
		"stealth.deployment_id":        *job.App.DesiredDeploymentID,
		"stealth.generation":           strconv.FormatInt(job.App.DesiredGeneration, 10),
		"stealth.workload_spec_sha256": job.App.WorkloadSpecSHA256,
		"stealth.runtime_schema":       runtimeSchema,
	}
	args := []string{"container", "create", "--name", job.ContainerName, "--network", networkName}
	for _, key := range []string{"stealth.managed", "stealth.resource_type", "stealth.app_id", "stealth.project_id", "stealth.deployment_id", "stealth.generation", "stealth.workload_spec_sha256", "stealth.runtime_schema"} {
		args = append(args, "--label", key+"="+labels[key])
	}
	args = append(args,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true",
		"--cpus", dockerCPUValue(spec.Resources.CPUMillis),
		"--memory", strconv.FormatInt(spec.Resources.MemoryBytes, 10),
		"--memory-swap", strconv.FormatInt(spec.Resources.MemoryBytes, 10),
		"--pids-limit", strconv.Itoa(spec.Resources.PIDsLimit),
		"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=67108864",
		"--restart", "no",
		"--log-driver", "json-file",
		"--log-opt", "max-size=10m",
		"--log-opt", "max-file=3",
		"--ulimit", "nofile=4096:4096",
		"--ulimit", "core=0:0",
		"--init",
	)
	if profile.Runtime != "" {
		args = append(args, "--runtime", profile.Runtime)
	}
	if spec.WorkingDirectory != nil {
		args = append(args, "--workdir", *spec.WorkingDirectory)
	}
	args = append(args, imageRef)
	args = append(args, spec.Command...)
	return args, nil
}

func ContainerLabels(job repository.AppRuntimeJob) (map[string]string, error) {
	appID, err := uuid.Parse(job.App.ID)
	if err != nil {
		return nil, ErrInvalidRuntimeJob
	}
	projectID, err := uuid.Parse(job.App.ProjectID)
	if err != nil || job.App.DesiredDeploymentID == nil {
		return nil, ErrInvalidRuntimeJob
	}
	if _, err := uuid.Parse(*job.App.DesiredDeploymentID); err != nil {
		return nil, ErrInvalidRuntimeJob
	}
	return map[string]string{
		"stealth.managed":              "true",
		"stealth.resource_type":        "app",
		"stealth.app_id":               appID.String(),
		"stealth.project_id":           projectID.String(),
		"stealth.deployment_id":        *job.App.DesiredDeploymentID,
		"stealth.generation":           strconv.FormatInt(job.App.DesiredGeneration, 10),
		"stealth.workload_spec_sha256": job.App.WorkloadSpecSHA256,
		"stealth.runtime_schema":       runtimeSchema,
	}, nil
}

func ContainerMatchesDesired(container Container, job repository.AppRuntimeJob, image Image, networkName string) bool {
	return strings.TrimPrefix(container.Name, "/") == job.ContainerName && ContainerMatchesDesiredExceptName(container, job, image, networkName)
}

func ContainerMatchesDesiredExceptName(container Container, job repository.AppRuntimeJob, image Image, networkName string) bool {
	labels, err := ContainerLabels(job)
	if err != nil || !managedForApp(container, uuid.MustParse(job.App.ID), uuid.MustParse(job.App.ProjectID)) || !hasLabels(container.Config.Labels, labels) {
		return false
	}
	if container.ImageID != image.ID || container.HostConfig.NetworkMode != networkName || container.HostConfig.Privileged || container.HostConfig.AutoRemove || len(container.HostConfig.CapAdd) != 0 || !container.HostConfig.ReadonlyRootfs ||
		!slices.Contains(container.HostConfig.CapDrop, "ALL") || !slices.Contains(container.HostConfig.SecurityOpt, "no-new-privileges:true") ||
		container.HostConfig.Memory != job.App.Workload.Resources.MemoryBytes || container.HostConfig.MemorySwap != job.App.Workload.Resources.MemoryBytes ||
		container.HostConfig.NanoCpus != int64(job.App.Workload.Resources.CPUMillis)*1_000_000 || container.HostConfig.PidsLimit == nil || *container.HostConfig.PidsLimit != int64(job.App.Workload.Resources.PIDsLimit) ||
		container.HostConfig.RestartPolicy.Name != "no" || container.HostConfig.LogConfig.Type != "json-file" ||
		container.HostConfig.LogConfig.Config["max-size"] != "10m" || container.HostConfig.LogConfig.Config["max-file"] != "3" ||
		container.HostConfig.NetworkMode == "host" || container.HostConfig.PidMode == "host" || container.HostConfig.IpcMode == "host" ||
		container.HostConfig.UTSMode == "host" || container.HostConfig.UsernsMode == "host" || len(container.HostConfig.Binds) != 0 ||
		len(container.HostConfig.VolumesFrom) != 0 || len(container.HostConfig.PortBindings) != 0 || len(container.HostConfig.Devices) != 0 ||
		!exactTmpfs(container.HostConfig.Tmpfs) || !noUnexpectedMounts(container.Mounts) || !hasUlimits(container.HostConfig.Ulimits) || container.HostConfig.Init == nil || !*container.HostConfig.Init {
		return false
	}
	if len(container.Networks) != 1 {
		return false
	}
	if _, ok := container.Networks[networkName]; !ok {
		return false
	}
	command := job.App.Workload.Command
	if len(command) == 0 {
		command = image.Command
	}
	workingDir := image.WorkingDir
	if job.App.Workload.WorkingDirectory != nil {
		workingDir = *job.App.Workload.WorkingDirectory
	}
	return slices.Equal(container.Config.Cmd, command) && slices.Equal(container.Config.Entrypoint, image.Entrypoint) &&
		slices.Equal(container.Config.Env, image.Environment) && container.Config.WorkingDir == workingDir && container.Config.User == image.User
}

func managedAppContainer(container Container) bool {
	_, _, valid := validManagedAppContainerIdentity(container)
	return valid
}

func managedForApp(container Container, appID, projectID uuid.UUID) bool {
	if !managedAppContainer(container) || container.Config.Labels["stealth.app_id"] != appID.String() || container.Config.Labels["stealth.project_id"] != projectID.String() {
		return false
	}
	return repository.ValidAppRuntimeContainerName(appID, strings.TrimPrefix(container.Name, "/"))
}

func validRuntimeNameForApp(appID uuid.UUID, name string) bool {
	return repository.ValidAppRuntimeContainerName(appID, name)
}

func validManagedAppContainerIdentity(container Container) (uuid.UUID, uuid.UUID, bool) {
	labels := container.Config.Labels
	appID, appErr := uuid.Parse(labels["stealth.app_id"])
	projectID, projectErr := uuid.Parse(labels["stealth.project_id"])
	deploymentID, deploymentErr := uuid.Parse(labels["stealth.deployment_id"])
	generation, generationErr := strconv.ParseInt(labels["stealth.generation"], 10, 64)
	if labels["stealth.managed"] != "true" || labels["stealth.resource_type"] != "app" || labels["stealth.runtime_schema"] != runtimeSchema ||
		appErr != nil || projectErr != nil || deploymentErr != nil ||
		appID == uuid.Nil || projectID == uuid.Nil || deploymentID == uuid.Nil ||
		labels["stealth.app_id"] != appID.String() || labels["stealth.project_id"] != projectID.String() || labels["stealth.deployment_id"] != deploymentID.String() ||
		generation < 1 || generationErr != nil || strconv.FormatInt(generation, 10) != labels["stealth.generation"] ||
		!validWorkloadSpecDigest(labels["stealth.workload_spec_sha256"]) || !validRuntimeID(container.ID) ||
		!validManagedDockerContainerName(container.Name) {
		return uuid.Nil, uuid.Nil, false
	}
	return appID, projectID, true
}

func validManagedDockerContainerName(value string) bool {
	if len(value) < 2 || len(value) > 256 || value[0] != '/' || strings.Contains(value[1:], "/") {
		return false
	}
	for _, character := range value[1:] {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && !strings.ContainsRune("_.-", character) {
			return false
		}
	}
	return true
}

func validWorkloadSpecDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func hasLabels(actual, expected map[string]string) bool {
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func exactTmpfs(value map[string]string) bool {
	return len(value) == 1 && value["/tmp"] == "rw,nosuid,nodev,noexec,size=67108864"
}

func noUnexpectedMounts(mounts []containerMount) bool {
	// Docker reports explicit volumes and bind mounts here. Tmpfs mounts are
	// configured separately through HostConfig.Tmpfs and do not appear in this
	// list on Moby.
	return len(mounts) == 0
}

func hasUlimits(values []struct {
	Name string `json:"Name"`
	Soft int64  `json:"Soft"`
	Hard int64  `json:"Hard"`
}) bool {
	var fileLimit, coreLimit bool
	for _, value := range values {
		switch value.Name {
		case "nofile":
			fileLimit = value.Soft == 4096 && value.Hard == 4096
		case "core":
			coreLimit = value.Soft == 0 && value.Hard == 0
		}
	}
	return fileLimit && coreLimit
}

func dockerCPUValue(millis int) string {
	whole, fraction := millis/1000, millis%1000
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	value := fmt.Sprintf("%d.%03d", whole, fraction)
	return strings.TrimRight(strings.TrimRight(value, "0"), ".")
}

func validDockerName(value string) bool {
	if len(value) < 1 || len(value) > 63 || value != strings.ToLower(value) {
		return false
	}
	for index, character := range value {
		valid := (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.'
		if !valid || (index == 0 && !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9'))) {
			return false
		}
	}
	return true
}

func validImageTag(value string) bool {
	if len(value) < 1 || len(value) > 255 || strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= '0' && character <= '9') && !strings.ContainsRune("._:/-", character) {
			return false
		}
	}
	return true
}

func validImageID(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && validDigest(value[7:])
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validRuntimeID(value string) bool {
	if len(value) < 12 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func hostPlatform() string {
	if runtime.GOOS != "linux" {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return "linux/" + runtime.GOARCH
}

func IsDockerNotFound(err error) bool { return errors.Is(err, ErrDockerObjectNotFound) }

func IsOwnershipConflict(err error) bool {
	return errors.Is(err, ErrRuntimeOwnershipConflict) || errors.Is(err, ErrRuntimeNetworkConflict)
}

func safeRuntimeError(err error) string {
	switch {
	case errors.Is(err, ErrRuntimeNetworkConflict):
		return "runtime network conflict"
	case errors.Is(err, ErrRuntimeOwnershipConflict):
		return "container ownership conflict"
	case errors.Is(err, ErrImageVerification), errors.Is(err, ociartifact.ErrInvalidArchive):
		return "image verification failed"
	case errors.Is(err, ErrImageArtifactUnavailable), errors.Is(err, ErrDockerObjectNotFound):
		return "image artifact unavailable"
	case errors.Is(err, errUnsupportedImageVolumes):
		return "runtime image declares unsupported volumes"
	case errors.Is(err, ErrUnsupportedRuntimePlatform):
		return "unsupported runtime platform"
	case errors.Is(err, ErrImageImport):
		return "image import failed"
	case errors.Is(err, ErrContainerCreate):
		return "container create failed"
	case errors.Is(err, ErrContainerStart):
		return "container start failed"
	case errors.Is(err, ErrContainerInspection):
		return "container inspection failed"
	case errors.Is(err, ErrRuntimeUnavailable), errors.Is(err, context.DeadlineExceeded):
		return "runtime unavailable"
	default:
		return "runtime unavailable"
	}
}

func runtimeTagForJob(job repository.AppRuntimeJob) (string, error) {
	if job.Deployment.ID == "" {
		return "", ErrImageVerification
	}
	deploymentID, err := uuid.Parse(job.Deployment.ID)
	if err != nil {
		return "", ErrImageVerification
	}
	return ImageTag(deploymentID), nil
}

func deploymentDigest(job repository.AppRuntimeJob) (string, error) {
	if job.Deployment.ImageDigest == nil || !strings.HasPrefix(*job.Deployment.ImageDigest, "sha256:") || !validDigest((*job.Deployment.ImageDigest)[7:]) {
		return "", ErrImageVerification
	}
	return *job.Deployment.ImageDigest, nil
}
