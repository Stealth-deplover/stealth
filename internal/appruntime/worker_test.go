package appruntime

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/appsecret"
	"github.com/Stealth-deplover/stealth/internal/appstore"
	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

type runtimeTestStore struct {
	current              bool
	completeStatus       string
	completed            *repository.AppRuntimeContainer
	failureStatus        string
	failureMessage       string
	released             int
	queueCalls           int
	queuedAppID          uuid.UUID
	queuedProjectID      *uuid.UUID
	queuedContainerID    string
	queuedContainerName  string
	completeCalls        int
	failureCalls         int
	completeErr          error
	currentErr           error
	observedGeneration   int64
	cleanupCompleteCall  int
	resetCalls           int
	resetIdentity        uuid.UUID
	createIdentity       uuid.UUID
	runtimeContainerID   string
	environment          []repository.AppRuntimeEnvironmentCiphertext
	protectedDeployments []uuid.UUID
	protectedQuerySizes  []int
	activeRuntimeLease   bool
	pruneCleanupCalls    int
}

func (s *runtimeTestStore) ScheduleAppRuntimeStartupSweep(context.Context) error { return nil }
func (s *runtimeTestStore) RequeueStaleAppRuntimeLeases(context.Context) (int64, error) {
	return 0, nil
}
func (s *runtimeTestStore) ClaimNextAppRuntime(context.Context, string, time.Duration) (repository.AppRuntimeJob, error) {
	return repository.AppRuntimeJob{}, repository.ErrNoAppRuntimeJob
}
func (s *runtimeTestStore) ListAppRuntimeEnvironment(context.Context, repository.AppRuntimeJob) ([]repository.AppRuntimeEnvironmentCiphertext, error) {
	return s.environment, nil
}
func (s *runtimeTestStore) RenewAppRuntimeLease(context.Context, uuid.UUID, string, uuid.UUID, time.Duration) error {
	return nil
}
func (s *runtimeTestStore) IsAppRuntimeJobCurrent(context.Context, repository.AppRuntimeJob) (bool, error) {
	return s.current, s.currentErr
}
func (s *runtimeTestStore) ReleaseAppRuntimeJob(context.Context, repository.AppRuntimeJob) error {
	s.released++
	return nil
}
func (s *runtimeTestStore) CompleteAppRuntime(_ context.Context, job repository.AppRuntimeJob, status string, container *repository.AppRuntimeContainer) error {
	s.completeCalls++
	if s.completeErr != nil {
		return s.completeErr
	}
	s.completeStatus = status
	s.completed = container
	s.observedGeneration = job.App.DesiredGeneration
	return nil
}
func (s *runtimeTestStore) ResetAppHealthBeforeRuntimeRestart(_ context.Context, _ repository.AppRuntimeJob, identity uuid.UUID) error {
	s.resetCalls++
	s.resetIdentity = identity
	return nil
}
func (s *runtimeTestStore) RotateAppRuntimeIdentityBeforeCreate(_ context.Context, _ repository.AppRuntimeJob, identity uuid.UUID) error {
	s.createIdentity = identity
	return nil
}
func (s *runtimeTestStore) FailAppRuntime(_ context.Context, _ repository.AppRuntimeJob, status, message string, _ time.Time) error {
	s.failureCalls++
	s.failureStatus = status
	s.failureMessage = message
	return nil
}
func (s *runtimeTestStore) ClaimNextAppHealthCheck(context.Context, string, time.Duration) (repository.AppHealthCheckJob, error) {
	return repository.AppHealthCheckJob{}, repository.ErrNoAppHealthCheckJob
}
func (s *runtimeTestStore) IsAppHealthCheckCurrent(context.Context, repository.AppHealthCheckJob) (bool, error) {
	return s.current, s.currentErr
}
func (s *runtimeTestStore) CompleteAppHealthCheck(context.Context, repository.AppHealthCheckJob, bool) error {
	return nil
}
func (s *runtimeTestStore) InvalidateAppHealthIdentity(context.Context, repository.AppHealthCheckJob) error {
	return nil
}
func (s *runtimeTestStore) ReleaseAppHealthCheck(context.Context, repository.AppHealthCheckJob) error {
	s.released++
	return nil
}
func (s *runtimeTestStore) AppRuntimeContainerExists(_ context.Context, _ uuid.UUID, _ uuid.UUID, containerID string) (bool, error) {
	return containerID != "" && containerID == s.runtimeContainerID, nil
}
func (s *runtimeTestStore) ListProtectedAppRuntimeDeploymentIDs(_ context.Context, candidates []uuid.UUID) ([]uuid.UUID, bool, error) {
	s.protectedQuerySizes = append(s.protectedQuerySizes, len(candidates))
	candidateSet := make(map[uuid.UUID]struct{}, len(candidates))
	for _, id := range candidates {
		candidateSet[id] = struct{}{}
	}
	protected := make([]uuid.UUID, 0, len(s.protectedDeployments))
	for _, id := range s.protectedDeployments {
		if _, ok := candidateSet[id]; ok {
			protected = append(protected, id)
		}
	}
	return protected, s.activeRuntimeLease, nil
}

func (s *runtimeTestStore) QueueAppRuntimeCleanup(_ context.Context, projectID *uuid.UUID, appID uuid.UUID, containerID, containerName string, _ int) error {
	s.queueCalls++
	s.queuedAppID = appID
	s.queuedProjectID = projectID
	s.queuedContainerID = containerID
	s.queuedContainerName = containerName
	return nil
}
func (s *runtimeTestStore) ClaimNextAppRuntimeCleanup(context.Context, string, time.Duration) (repository.AppRuntimeCleanupJob, error) {
	return repository.AppRuntimeCleanupJob{}, repository.ErrNoAppRuntimeCleanup
}
func (s *runtimeTestStore) RenewAppRuntimeCleanupLease(context.Context, repository.AppRuntimeCleanupJob, time.Duration) error {
	return nil
}
func (s *runtimeTestStore) CompleteAppRuntimeCleanup(context.Context, repository.AppRuntimeCleanupJob) error {
	s.cleanupCompleteCall++
	return nil
}
func (s *runtimeTestStore) FailAppRuntimeCleanup(context.Context, repository.AppRuntimeCleanupJob, string, time.Time, bool) error {
	return nil
}
func (s *runtimeTestStore) PruneAppRuntimeCleanupJobs(context.Context, int) (int64, error) {
	s.pruneCleanupCalls++
	return 0, nil
}

func TestRuntimeEnvironmentDecryptsOnlyAppBoundCiphertextAndClearsPlaintext(t *testing.T) {
	job := runtimeTestJob()
	projectID := uuid.MustParse(job.App.ProjectID)
	appID := uuid.MustParse(job.App.ID)
	variableID := uuid.Must(uuid.NewV7())
	cipher, err := appsecret.New(bytes.Repeat([]byte{0x5a}, appsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("runtime-value")
	ciphertext, err := cipher.Encrypt(projectID, appID, variableID, secret)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeTestStore{environment: []repository.AppRuntimeEnvironmentCiphertext{{ID: variableID, Key: "API_TOKEN", Ciphertext: ciphertext}}}
	worker := &Worker{Store: store, AppSecretsCipher: cipher}
	values, err := worker.runtimeEnvironment(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Key != "API_TOKEN" || string(values[0].Value) != string(secret) {
		t.Fatalf("decrypted environment = %#v", values)
	}
	wipeRuntimeEnvironment(values)
	if len(values[0].Value) != 0 {
		t.Fatal("plaintext buffer remained available after cleanup")
	}
	store.environment[0].Ciphertext[len(store.environment[0].Ciphertext)-1] ^= 1
	if _, err := worker.runtimeEnvironment(context.Background(), job); !errors.Is(err, ErrAppEnvironmentDecryption) {
		t.Fatalf("tampered ciphertext error = %v", err)
	}
}

func TestWorkerRejectsAggregateRuntimeEnvironmentBeforeContainerCreate(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
	projectID, appID := uuid.MustParse(job.App.ProjectID), uuid.MustParse(job.App.ID)
	cipher, err := appsecret.New(bytes.Repeat([]byte{0x38}, appsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	worker.AppSecretsCipher = cipher
	value := bytes.Repeat([]byte("v"), repository.AppEnvironmentVariableMaxValueBytes)
	store.environment = make([]repository.AppRuntimeEnvironmentCiphertext, 0, 9)
	for index := 0; index < 8; index++ {
		id := uuid.Must(uuid.NewV7())
		encoded, err := cipher.Encrypt(projectID, appID, id, value)
		if err != nil {
			t.Fatal(err)
		}
		store.environment = append(store.environment, repository.AppRuntimeEnvironmentCiphertext{ID: id, Key: fmt.Sprintf("VALUE_%d", index), Ciphertext: encoded})
	}
	values, err := worker.runtimeEnvironment(context.Background(), job)
	if err != nil || len(values) != 8 {
		t.Fatalf("worker rejected environment at aggregate limit: values=%d err=%v", len(values), err)
	}
	wipeRuntimeEnvironment(values)
	extraID := uuid.Must(uuid.NewV7())
	extraCiphertext, err := cipher.Encrypt(projectID, appID, extraID, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	store.environment = append(store.environment, repository.AppRuntimeEnvironmentCiphertext{ID: extraID, Key: "VALUE_EXTRA", Ciphertext: extraCiphertext})

	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if driver.createCalls != 0 || driver.startCalls != 0 || store.completeCalls != 0 {
		t.Fatalf("over-limit runtime environment reached container execution: create=%d start=%d complete=%d", driver.createCalls, driver.startCalls, store.completeCalls)
	}
	if store.failureCalls != 1 || store.failureMessage != "runtime unavailable" {
		t.Fatalf("worker did not record a sanitized failure: calls=%d message=%q", store.failureCalls, store.failureMessage)
	}
}

type runtimeTestDriver struct {
	store              *runtimeTestStore
	image              Image
	container          Container
	found              bool
	ensureNetworkErr   error
	ensureImageErr     error
	inspectErr         error
	createErr          error
	startErr           error
	removeErr          error
	ensureImageCalls   int
	createCalls        int
	startCalls         int
	startResetCalls    int
	startedContainer   string
	renameCalls        int
	renamedFrom        string
	renamedTo          string
	removeCalls        int
	removeTargets      []string
	networkCalls       int
	createJob          repository.AppRuntimeJob
	createdEnvironment []RuntimeEnvironmentVariable
	loadedBytes        []byte
	onCreate           func()
	onRename           func()
	nextContainerID    string
	listedContainers   []Container
	listContainersErr  error
	listContainersCall int
	runtimeImageCache  []RuntimeImageCacheEntry
	containerImageRefs []ManagedAppImageReference
	removedImageTags   []string
	listImageCacheErr  error
	listImageRefsErr   error
	removeImageTagErr  error
	removeCleanupCall  int
}

func (r *runtimeTestDriver) EnsureNetwork(context.Context) error {
	r.networkCalls++
	return r.ensureNetworkErr
}
func (r *runtimeTestDriver) EnsureRuntimeNetworkPeers(context.Context) error { return nil }
func (r *runtimeTestDriver) EnsureImage(_ context.Context, info ociartifact.ImageInfo, archive io.ReadSeeker, tag string) (Image, error) {
	r.ensureImageCalls++
	if r.ensureImageErr != nil {
		return Image{}, r.ensureImageErr
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return Image{}, err
	}
	r.loadedBytes, _ = io.ReadAll(archive)
	image := r.image
	image.ID = info.ConfigDigest
	image.Tag = tag
	image.OS = info.OS
	image.Architecture = info.Architecture
	image.Variant = info.Variant
	image.Layers = append([]string(nil), info.LayerDiffIDs...)
	return image, nil
}
func (r *runtimeTestDriver) InspectApp(context.Context, uuid.UUID) (Container, bool, error) {
	return r.container, r.found, r.inspectErr
}
func (r *runtimeTestDriver) ProbeApp(context.Context, repository.AppHealthCheckJob, Container) error {
	return nil
}
func (r *runtimeTestDriver) CreateApp(_ context.Context, job repository.AppRuntimeJob, image Image, environment []RuntimeEnvironmentVariable) (Container, error) {
	r.createCalls++
	r.createJob = job
	r.createdEnvironment = make([]RuntimeEnvironmentVariable, len(environment))
	for index, item := range environment {
		r.createdEnvironment[index] = RuntimeEnvironmentVariable{Key: item.Key, Value: append([]byte(nil), item.Value...)}
	}
	if r.createErr != nil {
		return Container{}, r.createErr
	}
	if r.nextContainerID == "" {
		r.nextContainerID = strings.Repeat("b", 64)
	}
	r.container = runtimeTestContainer(job, image, false, r.nextContainerID)
	r.found = true
	if r.onCreate != nil {
		r.onCreate()
	}
	return r.container, nil
}
func (r *runtimeTestDriver) RenameApp(_ context.Context, job repository.AppRuntimeJob, containerID, targetName string) (Container, error) {
	r.renameCalls++
	r.renamedFrom = strings.TrimPrefix(r.container.Name, "/")
	r.renamedTo = targetName
	if !r.found || r.container.ID != containerID {
		return Container{}, ErrRuntimeOwnershipConflict
	}
	r.container.Name = "/" + targetName
	if r.onRename != nil {
		r.onRename()
	}
	return r.container, nil
}
func (r *runtimeTestDriver) StartApp(_ context.Context, job repository.AppRuntimeJob, containerID string) (Container, error) {
	r.startCalls++
	r.startedContainer = containerID
	if r.store != nil {
		r.startResetCalls = r.store.resetCalls
	}
	if r.startErr != nil {
		return Container{}, r.startErr
	}
	if !r.found || r.container.ID != containerID {
		return Container{}, ErrContainerStart
	}
	r.container = runtimeTestContainer(job, r.imageForJob(job), true, containerID)
	return r.container, nil
}
func (r *runtimeTestDriver) RemoveApp(_ context.Context, _ repository.AppRuntimeJob, containerID string) error {
	r.removeCalls++
	r.removeTargets = append(r.removeTargets, containerID)
	if r.removeErr != nil {
		return r.removeErr
	}
	if r.found && r.container.ID != containerID {
		return nil
	}
	r.found = false
	r.container = Container{}
	return nil
}
func (r *runtimeTestDriver) RemoveCleanupTarget(context.Context, repository.AppRuntimeCleanupJob) error {
	r.removeCleanupCall++
	return nil
}
func (r *runtimeTestDriver) ListManagedAppContainers(context.Context) ([]Container, error) {
	r.listContainersCall++
	return append([]Container(nil), r.listedContainers...), r.listContainersErr
}
func (r *runtimeTestDriver) ListRuntimeImageCache(context.Context) ([]RuntimeImageCacheEntry, error) {
	return append([]RuntimeImageCacheEntry(nil), r.runtimeImageCache...), r.listImageCacheErr
}
func (r *runtimeTestDriver) RemoveRuntimeImageTag(_ context.Context, entry RuntimeImageCacheEntry) error {
	r.removedImageTags = append(r.removedImageTags, entry.Reference)
	if r.removeImageTagErr == nil {
		for index, cached := range r.runtimeImageCache {
			if cached.Reference == entry.Reference {
				r.runtimeImageCache = append(r.runtimeImageCache[:index], r.runtimeImageCache[index+1:]...)
				break
			}
		}
	}
	return r.removeImageTagErr
}
func (r *runtimeTestDriver) ListManagedAppImageReferences(context.Context) ([]ManagedAppImageReference, error) {
	return append([]ManagedAppImageReference(nil), r.containerImageRefs...), r.listImageRefsErr
}
func (r *runtimeTestDriver) imageForJob(job repository.AppRuntimeJob) Image {
	image := r.image
	if job.Deployment.ImageDigest != nil {
		image.ID = runtimeTestConfigDigest(r.loadedBytes)
	}
	return image
}

func TestWorkerImportsVerifiedImageAndAppliesCurrentWorkload(t *testing.T) {
	worker, store, driver, job, archive := newRuntimeWorkerFixture(t, false)
	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.failureCalls != 0 || store.completeCalls != 1 || store.completeStatus != "running" || store.completed == nil {
		t.Fatalf("runtime completion = status %q container %#v failures=%d", store.completeStatus, store.completed, store.failureCalls)
	}
	if store.observedGeneration != job.App.DesiredGeneration || store.completed.ImageDigest != *job.Deployment.ImageDigest || store.completed.ImageID != runtimeTestConfigDigest(archive) {
		t.Fatalf("completed runtime identity = %#v observed=%d", store.completed, store.observedGeneration)
	}
	if driver.ensureImageCalls != 1 || !bytes.Equal(driver.loadedBytes, archive) || driver.createCalls != 1 || driver.startCalls != 1 {
		t.Fatalf("image import/create/start calls = %d/%d/%d; archive match=%v", driver.ensureImageCalls, driver.createCalls, driver.startCalls, bytes.Equal(driver.loadedBytes, archive))
	}
	if store.createIdentity == uuid.Nil || driver.createJob.RouteIdentity != store.createIdentity || driver.createJob.ContainerName == job.ContainerName {
		t.Fatalf("new container did not receive a distinct persisted routing incarnation: prior=%s create=%+v identity=%s", job.ContainerName, driver.createJob, store.createIdentity)
	}
	if got := driver.createJob.App.Workload.Resources.CPUMillis; got != 750 {
		t.Fatalf("runtime used workload snapshot CPU %d, want mutable App CPU 750", got)
	}
	if got := driver.createJob.App.Workload.WorkingDirectory; got == nil || *got != "/srv/current" {
		t.Fatalf("runtime working directory = %v, want current App spec /srv/current", got)
	}
	if got := driver.createJob.Deployment.WorkloadSnapshot.Resources.CPUMillis; got != 500 {
		t.Fatalf("deployment provenance snapshot changed or was not kept: %d", got)
	}
}

func TestWorkerReimportsEvictedRollbackReleaseFromPersistedArtifact(t *testing.T) {
	worker, store, driver, job, archive := newRuntimeWorkerFixture(t, false)
	rollbackWorkload := workloadspec.Default()
	rollbackWorkload.Resources.CPUMillis = 500
	rollbackWorkloadDigest, err := workloadspec.Digest(rollbackWorkload)
	if err != nil {
		t.Fatal(err)
	}
	job.App.Workload = rollbackWorkload
	job.App.WorkloadSpecSHA256 = rollbackWorkloadDigest
	job.App.DesiredGeneration = 6
	job.App.ObservedGeneration = 5
	job.Deployment.Version = 1
	job.Deployment.WorkloadSnapshot = rollbackWorkload
	job.Deployment.WorkloadSpecSHA256 = rollbackWorkloadDigest

	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.failureCalls != 0 || store.completeCalls != 1 || store.completeStatus != "running" || store.completed == nil {
		t.Fatalf("rollback runtime completion = status %q container %#v failures=%d", store.completeStatus, store.completed, store.failureCalls)
	}
	if driver.ensureImageCalls != 1 || !bytes.Equal(driver.loadedBytes, archive) {
		t.Fatalf("missing runtime cache was not restored from the persisted rollback archive: calls=%d archive_match=%v", driver.ensureImageCalls, bytes.Equal(driver.loadedBytes, archive))
	}
	if driver.createCalls != 1 || driver.startCalls != 1 || driver.createJob.App.Workload.Resources.CPUMillis != 500 {
		t.Fatalf("rollback WorkloadSpec was not used for a new runtime: create=%d start=%d cpu=%d", driver.createCalls, driver.startCalls, driver.createJob.App.Workload.Resources.CPUMillis)
	}
	if store.observedGeneration != 6 || store.completed.ImageDigest == "" || store.completed.ImageID != runtimeTestConfigDigest(archive) {
		t.Fatalf("rollback generation or verified image identity was not applied: observed=%d container=%+v", store.observedGeneration, store.completed)
	}
}

func TestWorkerRejectsMissingOrModifiedImageWithoutAdvancingGeneration(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*repository.AppRuntimeJob)
		want   string
	}{
		{name: "missing artifact", mutate: func(job *repository.AppRuntimeJob) {
			job.ImagePath = uuid.Must(uuid.NewV7()).String() + "/" + uuid.Must(uuid.NewV7()).String() + "/" + uuid.Must(uuid.NewV7()).String()
		}, want: "image artifact unavailable"},
		{name: "archive checksum mismatch", mutate: func(job *repository.AppRuntimeJob) {
			wrong := strings.Repeat("0", 64)
			job.Deployment.ImageArchiveSHA256 = &wrong
		}, want: "image verification failed"},
		{name: "manifest digest mismatch", mutate: func(job *repository.AppRuntimeJob) {
			wrong := "sha256:" + strings.Repeat("f", 64)
			job.Deployment.ImageDigest = &wrong
		}, want: "image verification failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
			job.App.ObservedGeneration = job.App.DesiredGeneration - 1
			test.mutate(&job)
			if err := worker.processApp(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if store.completeCalls != 0 || store.observedGeneration != job.App.ObservedGeneration || store.failureCalls != 1 || store.failureStatus != "failed" || store.failureMessage != test.want {
				t.Fatalf("failed image changed observed truth: complete=%d observed=%d failure=%+v", store.completeCalls, store.observedGeneration, store)
			}
			if driver.ensureImageCalls != 0 || driver.createCalls != 0 || driver.startCalls != 0 {
				t.Fatalf("unverified artifact reached Moby: import/create/start=%d/%d/%d", driver.ensureImageCalls, driver.createCalls, driver.startCalls)
			}
		})
	}
}

func TestWorkerGenerationRaceRemovesUncommittedContainer(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
	driver.onCreate = func() { store.current = false }
	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.completeCalls != 0 || store.failureCalls != 0 || store.released != 1 || store.observedGeneration != job.App.ObservedGeneration {
		t.Fatalf("stale generation finalized: completed=%d failed=%d released=%d observed=%d", store.completeCalls, store.failureCalls, store.released, store.observedGeneration)
	}
	if driver.createCalls != 1 || driver.removeCalls != 1 || driver.found {
		t.Fatalf("stale created container was not removed: create=%d remove=%d remains=%v", driver.createCalls, driver.removeCalls, driver.found)
	}
	if len(driver.removeTargets) != 1 || driver.removeTargets[0] != strings.Repeat("a", 64) {
		t.Fatalf("stale cleanup did not target the exact uncommitted container id: %v", driver.removeTargets)
	}
}

func TestWorkerRejectsForeignContainerWithoutAdoptingOrRemoving(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
	foreign := job
	foreign.App.ID = uuid.Must(uuid.NewV7()).String()
	driver.container = runtimeTestContainer(foreign, driver.image, true, strings.Repeat("c", 64))
	driver.container.Name = "/" + repository.AppRuntimeContainerName(uuid.MustParse(job.App.ID))
	driver.found = true
	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.failureStatus != "failed" || store.failureMessage != "container ownership conflict" || store.observedGeneration != job.App.ObservedGeneration {
		t.Fatalf("foreign container conflict state = %+v", store)
	}
	if driver.createCalls != 0 || driver.removeCalls != 0 || !driver.found || driver.container.Config.Labels["stealth.app_id"] == job.App.ID {
		t.Fatalf("foreign container was adopted or removed: create=%d remove=%d found=%v labels=%v", driver.createCalls, driver.removeCalls, driver.found, driver.container.Config.Labels)
	}
}

func TestWorkerReusesExactContainerAndReplacesGenerationDrift(t *testing.T) {
	t.Run("exact running container", func(t *testing.T) {
		worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
		image := driver.image
		driver.container = runtimeTestContainer(job, image, true, strings.Repeat("d", 64))
		driver.found = true
		if err := worker.processApp(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		if store.completeCalls != 1 || driver.createCalls != 0 || driver.startCalls != 0 || driver.removeCalls != 0 {
			t.Fatalf("exact runtime was not adopted as already converged: completed=%d create/start/remove=%d/%d/%d", store.completeCalls, driver.createCalls, driver.startCalls, driver.removeCalls)
		}
	})

	t.Run("stale generation", func(t *testing.T) {
		worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
		staleJob := job
		staleJob.App.DesiredGeneration--
		staleLabels, err := ContainerLabels(staleJob)
		if err != nil {
			t.Fatal(err)
		}
		image := driver.image
		driver.container = runtimeTestContainer(staleJob, image, true, strings.Repeat("e", 64))
		driver.container.Config.Labels = staleLabels
		driver.found = true
		if err := worker.processApp(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		if store.completeCalls != 1 || store.completeStatus != "running" || driver.removeCalls != 1 || driver.createCalls != 1 || driver.startCalls != 1 {
			t.Fatalf("stale runtime was not replaced: completed=%d status=%s remove/create/start=%d/%d/%d", store.completeCalls, store.completeStatus, driver.removeCalls, driver.createCalls, driver.startCalls)
		}
		if store.createIdentity == uuid.Nil || driver.createJob.RouteIdentity != store.createIdentity || driver.createJob.ContainerName == staleJob.ContainerName {
			t.Fatalf("generation replacement reused the old routing identity: old=%s create=%+v identity=%s", staleJob.ContainerName, driver.createJob, store.createIdentity)
		}
	})
}

func TestWorkerResetsHealthBeforeRestartingSameContainer(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
	containerID := strings.Repeat("a", 64)
	driver.container = runtimeTestContainer(job, driver.image, false, containerID)
	driver.found = true

	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.resetCalls != 1 || driver.startCalls != 1 || driver.startResetCalls != 1 {
		t.Fatalf("restart did not durably invalidate health before Docker start: resets=%d starts=%d resets-before-start=%d", store.resetCalls, driver.startCalls, driver.startResetCalls)
	}
	if driver.startedContainer != containerID || driver.container.ID != containerID {
		t.Fatalf("restart replaced the managed container identity: started=%q after=%q want=%q", driver.startedContainer, driver.container.ID, containerID)
	}
	if store.resetIdentity == uuid.Nil || driver.renameCalls != 1 || driver.renamedFrom != job.ContainerName || driver.renamedTo == job.ContainerName || driver.container.Name != "/"+driver.renamedTo {
		t.Fatalf("restart did not rotate DNS target before starting: identity=%s rename=%d %q -> %q current=%q", store.resetIdentity, driver.renameCalls, driver.renamedFrom, driver.renamedTo, driver.container.Name)
	}
	if store.completeCalls != 1 || store.completeStatus != "running" || store.completed == nil || store.completed.ID != containerID {
		t.Fatalf("same-container restart did not complete current runtime: status=%q container=%+v calls=%d", store.completeStatus, store.completed, store.completeCalls)
	}
}

func TestWorkerDoesNotStartSameContainerAfterLeaseExpiresDuringRename(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
	driver.container = runtimeTestContainer(job, driver.image, false, strings.Repeat("a", 64))
	driver.found = true
	driver.onRename = func() { store.current = false }

	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.resetCalls != 1 || driver.renameCalls != 1 || driver.startCalls != 0 || store.completeCalls != 0 {
		t.Fatalf("expired runtime lease allowed a same-container restart: reset=%d rename=%d start=%d complete=%d", store.resetCalls, driver.renameCalls, driver.startCalls, store.completeCalls)
	}
}

func TestWorkerRemovesRuntimeForDisabledAndUnselectedApps(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*repository.AppRuntimeJob)
		wantStatus string
	}{
		{name: "disabled selected App", mutate: func(job *repository.AppRuntimeJob) { job.App.Enabled = false }, wantStatus: "stopped"},
		{name: "enabled without image", mutate: func(job *repository.AppRuntimeJob) { job.App.DesiredDeploymentID = nil }, wantStatus: "not_deployed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
			driver.container = runtimeTestContainer(job, driver.image, true, strings.Repeat("f", 64))
			driver.found = true
			test.mutate(&job)
			if err := worker.processApp(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if store.completeCalls != 1 || store.completeStatus != test.wantStatus || store.observedGeneration != job.App.DesiredGeneration || driver.removeCalls != 1 || driver.found {
				t.Fatalf("non-running desired state was not cleaned: status=%s complete=%d observed=%d remove=%d found=%v", store.completeStatus, store.completeCalls, store.observedGeneration, driver.removeCalls, driver.found)
			}
			if driver.ensureImageCalls != 0 || driver.createCalls != 0 || driver.startCalls != 0 {
				t.Fatalf("disabled/unselected App started an image: import/create/start=%d/%d/%d", driver.ensureImageCalls, driver.createCalls, driver.startCalls)
			}
		})
	}
}

func TestWorkerRetriesUnavailableMobyWithoutAdvancingObservedGeneration(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
	driver.ensureNetworkErr = fmt.Errorf("%w: daemon socket unavailable", ErrRuntimeUnavailable)
	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.failureCalls != 1 || store.failureStatus != "degraded" || store.failureMessage != "runtime unavailable" || store.completeCalls != 0 || store.observedGeneration != job.App.ObservedGeneration {
		t.Fatalf("runtime outage advanced or leaked runtime state: %+v", store)
	}
}

func TestWorkerRejectsImageDeclaredVolumesBeforeImport(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, true)
	if err := worker.processApp(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if store.failureStatus != "failed" || store.failureMessage != "runtime image declares unsupported volumes" || store.observedGeneration != job.App.ObservedGeneration {
		t.Fatalf("image volume policy result = %+v", store)
	}
	if driver.ensureImageCalls != 0 || driver.createCalls != 0 || driver.startCalls != 0 {
		t.Fatalf("volume image reached Moby import/container creation: %d/%d/%d", driver.ensureImageCalls, driver.createCalls, driver.startCalls)
	}
}

func TestSupportedRuntimePlatformChecksHostArchitectureAndAcceptsOCIArmVariant(t *testing.T) {
	host := runtimeArchitecture()
	image := ociartifact.ImageInfo{OS: "linux", Architecture: host, Variant: "v8"}
	if !supportedRuntimePlatform("linux/"+host, image) {
		t.Fatalf("host image with OCI variant was rejected: host=%s image=%+v", host, image)
	}
	other := "arm64"
	if host == "arm64" {
		other = "amd64"
	}
	if supportedRuntimePlatform("linux/"+other, ociartifact.ImageInfo{OS: "linux", Architecture: other}) {
		t.Fatalf("cross-architecture image was accepted on %s", host)
	}
}

func TestOrphanSweepCleansDuplicateManagedContainerForLiveApp(t *testing.T) {
	worker, store, driver, job, _ := newRuntimeWorkerFixture(t, false)
	duplicate := runtimeTestContainer(job, driver.image, true, strings.Repeat("9", 64))
	duplicate.Name = "/operator-renamed-duplicate"
	driver.listedContainers = []Container{duplicate}
	if err := worker.sweepOrphansIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.queueCalls != 1 || store.queuedAppID.String() != job.App.ID || store.queuedProjectID == nil || store.queuedProjectID.String() != job.App.ProjectID ||
		store.queuedContainerID != duplicate.ID || store.queuedContainerName != duplicate.Name {
		t.Fatalf("live App duplicate was not durably queued by validated Docker identity: %+v", store)
	}

	worker.nextOrphanSweep = time.Time{}
	driver.listedContainers = []Container{runtimeTestContainer(job, driver.image, true, strings.Repeat("8", 64))}
	if err := worker.sweepOrphansIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.queueCalls != 1 {
		t.Fatalf("canonical live App runtime was queued as an orphan: calls=%d", store.queueCalls)
	}
}

func TestOrphanSweepFailureUsesBoundedBackoff(t *testing.T) {
	worker, _, driver, _, _ := newRuntimeWorkerFixture(t, false)
	driver.listContainersErr = ErrRuntimeUnavailable
	now := time.Now()
	if err := worker.sweepOrphansIfDue(context.Background()); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("orphan inventory failure = %v", err)
	}
	if worker.orphanFailureCount != 1 || !worker.nextOrphanSweep.After(now) {
		t.Fatalf("orphan failure backoff state = count %d retry %s", worker.orphanFailureCount, worker.nextOrphanSweep)
	}
	if err := worker.sweepOrphansIfDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if driver.listContainersCall != 1 {
		t.Fatalf("orphan failure retried before its backoff: list calls=%d", driver.listContainersCall)
	}
	worker.nextOrphanSweep = time.Time{}
	if err := worker.sweepOrphansIfDue(context.Background()); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("second orphan inventory failure = %v", err)
	}
	if worker.orphanFailureCount != 2 {
		t.Fatalf("orphan failure count = %d, want 2", worker.orphanFailureCount)
	}
	if delay := boundedMaintenanceBackoff(time.Minute, 32, maxOrphanSweepBackoff); delay != maxOrphanSweepBackoff {
		t.Fatalf("orphan sweep backoff = %s, want cap %s", delay, maxOrphanSweepBackoff)
	}
}

func newRuntimeWorkerFixture(t *testing.T, withVolume bool) (*Worker, *runtimeTestStore, *runtimeTestDriver, repository.AppRuntimeJob, []byte) {
	t.Helper()
	appID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	projectID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	deploymentID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	archive, manifestDigest, configDigest := runtimeTestOCIArchive(t, withVolume)
	archiveHash := sha256.Sum256(archive)
	store, err := appstore.New(t.TempDir(), 1<<20, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.Images.BeginUpload(context.Background(), projectID, appID, deploymentID, bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Images.Commit(context.Background(), &prepared); err != nil {
		t.Fatal(err)
	}
	workload := workloadspec.Default()
	workload.Resources.CPUMillis = 750
	workingDirectory := "/srv/current"
	workload.WorkingDirectory = &workingDirectory
	workload.Command = []string{"serve", "literal; $(id)"}
	workloadDigest, err := workloadspec.Digest(workload)
	if err != nil {
		t.Fatal(err)
	}
	selected := deploymentID.String()
	imageDigest := manifestDigest
	archiveChecksum := hex.EncodeToString(archiveHash[:])
	imageSize := int64(len(archive))
	job := repository.AppRuntimeJob{
		App: domain.App{
			ID: appID.String(), ProjectID: projectID.String(), Name: "runtime-test", Enabled: true,
			Workload: workload, WorkloadSpecSHA256: workloadDigest, DesiredGeneration: 5,
			ObservedGeneration: 4, DesiredDeploymentID: &selected, RuntimeStatus: "pending",
		},
		Deployment: domain.AppDeployment{
			ID: deploymentID.String(), AppID: appID.String(), ProjectID: projectID.String(),
			Platform: "linux/" + runtimeArchitecture(), Status: "ready", BuildStatus: "succeeded",
			ImageDigest: &imageDigest, ImageArchiveSHA256: &archiveChecksum, ImageSizeBytes: &imageSize,
			WorkloadSnapshot: workloadspec.Default(), WorkloadSpecSHA256: strings.Repeat("b", 64),
		},
		ImagePath: prepared.RelativePath, WorkerID: "runtime-worker", LeaseToken: uuid.Must(uuid.NewV7()), FailureCount: 1,
	}
	job.RouteIdentity = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	job.ContainerName = repository.AppRuntimeContainerNameForIncarnation(appID, job.RouteIdentity)
	image := Image{
		ID: configDigest, OS: "linux", Architecture: runtimeArchitecture(),
		Entrypoint: []string{"/probe"}, Command: []string{"image-default"},
		Environment: []string{"FROM_IMAGE=kept"}, WorkingDir: "/image", User: "10001:10001",
	}
	persistence := &runtimeTestStore{current: true, observedGeneration: job.App.ObservedGeneration, runtimeContainerID: strings.Repeat("8", 64)}
	driver := &runtimeTestDriver{store: persistence, image: image, nextContainerID: strings.Repeat("a", 64)}
	worker := &Worker{
		Store: persistence, Artifacts: store, Runtime: driver, WorkerID: job.WorkerID,
		LeaseAge: time.Minute, MaxImageBytes: 1 << 20, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return worker, persistence, driver, job, archive
}

func runtimeTestContainer(job repository.AppRuntimeJob, image Image, running bool, id string) Container {
	labels, _ := ContainerLabels(job)
	command := job.App.Workload.Command
	if len(command) == 0 {
		command = image.Command
	}
	workingDirectory := image.WorkingDir
	if job.App.Workload.WorkingDirectory != nil {
		workingDirectory = *job.App.Workload.WorkingDirectory
	}
	pids := int64(job.App.Workload.Resources.PIDsLimit)
	initEnabled := true
	return Container{
		ID: id, Name: "/" + job.ContainerName, ImageID: image.ID,
		Config: containerConfig{
			Labels: labels, Cmd: append([]string(nil), command...), Entrypoint: append([]string(nil), image.Entrypoint...),
			Env: append([]string(nil), image.Environment...), WorkingDir: workingDirectory, User: image.User,
		},
		State: containerState{Status: map[bool]string{true: "running", false: "created"}[running], Running: running},
		HostConfig: hostConfig{
			ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"},
			NetworkMode: defaultRuntimeNetwork, Memory: job.App.Workload.Resources.MemoryBytes,
			MemorySwap: job.App.Workload.Resources.MemoryBytes, NanoCpus: int64(job.App.Workload.Resources.CPUMillis) * 1_000_000,
			PidsLimit: &pids, Tmpfs: map[string]string{"/tmp": "rw,nosuid,nodev,noexec,size=67108864"},
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
		Networks: map[string]ContainerNetwork{defaultRuntimeNetwork: {NetworkID: "network-id", IPAddress: "172.22.0.5"}},
	}
}

func runtimeTestOCIArchive(t *testing.T, withVolume bool) ([]byte, string, string) {
	t.Helper()
	config := map[string]any{
		"architecture": runtimeArchitecture(), "os": "linux",
		"config": map[string]any{
			"Entrypoint": []string{"/probe"}, "Cmd": []string{"image-default"},
			"Env": []string{"FROM_IMAGE=kept"}, "WorkingDir": "/image", "User": "10001:10001",
		},
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{}},
	}
	if withVolume {
		config["config"].(map[string]any)["Volumes"] = map[string]any{"/data": map[string]any{}}
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configDigest := runtimeTestDigest(configBytes)
	manifestBytes, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": configDigest, "size": len(configBytes)},
		"layers": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := runtimeTestDigest(manifestBytes)
	indexBytes, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": []any{map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": manifestDigest, "size": len(manifestBytes)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := []struct {
		name string
		data []byte
	}{
		{name: "oci-layout", data: []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{name: "index.json", data: indexBytes},
		{name: "blobs/sha256/" + strings.TrimPrefix(configDigest, "sha256:"), data: configBytes},
		{name: "blobs/sha256/" + strings.TrimPrefix(manifestDigest, "sha256:"), data: manifestBytes},
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, entry := range entries {
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes(), manifestDigest, configDigest
}

func runtimeTestDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func runtimeTestConfigDigest(archive []byte) string {
	info, err := ociartifact.Inspect(bytes.NewReader(archive), runtimeTestManifestDigest(archive), int64(len(archive)))
	if err != nil {
		return ""
	}
	return info.ConfigDigest
}

func runtimeTestManifestDigest(archive []byte) string {
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err != nil {
			return ""
		}
		if header.Name != "index.json" {
			continue
		}
		var index struct {
			Manifests []struct {
				Digest string `json:"digest"`
			} `json:"manifests"`
		}
		if json.NewDecoder(reader).Decode(&index) != nil || len(index.Manifests) != 1 {
			return ""
		}
		return index.Manifests[0].Digest
	}
}

func runtimeArchitecture() string {
	return runtime.GOARCH
}

var _ Persistence = (*runtimeTestStore)(nil)
var _ Runtime = (*runtimeTestDriver)(nil)

func TestNetworkRetryBackoffIsBoundedAndDeterministic(t *testing.T) {
	worker := &Worker{}
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	want := []time.Duration{
		time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		maxRuntimeRetry,
		maxRuntimeRetry,
	}
	for attempt, expected := range want {
		if delay := worker.deferNetworkRetry(now); delay != expected {
			t.Fatalf("attempt %d delay = %s, want %s", attempt+1, delay, expected)
		}
		if got := worker.networkRetryAfter; !got.Equal(now.Add(expected)) {
			t.Fatalf("attempt %d retry time = %s, want %s", attempt+1, got, now.Add(expected))
		}
	}
	if worker.networkFailureCount != 7 {
		t.Fatalf("failure count = %d, want capped at 7", worker.networkFailureCount)
	}
}
