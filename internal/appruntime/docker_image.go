package appruntime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Stealth-deplover/stealth/internal/ociartifact"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"io"
	"runtime"
	"slices"
	"strings"
)

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

func hostPlatform() string {
	if runtime.GOOS != "linux" {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return "linux/" + runtime.GOARCH
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
