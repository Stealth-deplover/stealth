package repository

import (
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/workloadspec"
	"github.com/google/uuid"
)

func TestAppRollbackArtifactReadinessRequiresCompleteCanonicalTarget(t *testing.T) {
	projectID, appID, deploymentID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	digest := "sha256:" + strings.Repeat("a", 64)
	checksum := strings.Repeat("b", 64)
	size := int64(128)
	path := projectID.String() + "/" + appID.String() + "/" + deploymentID.String()
	target := domain.AppDeployment{
		ImageDigest:        &digest,
		ImageArchiveSHA256: &checksum,
		ImageSizeBytes:     &size,
	}
	if !appRollbackArtifactReady(target, &path, projectID, appID, deploymentID) {
		t.Fatal("complete canonical deployment artifact was rejected")
	}
	for _, test := range []struct {
		name   string
		mutate func(*domain.AppDeployment, *string)
	}{
		{name: "missing digest", mutate: func(item *domain.AppDeployment, _ *string) { item.ImageDigest = nil }},
		{name: "invalid digest", mutate: func(item *domain.AppDeployment, _ *string) { invalid := "sha256:bad"; item.ImageDigest = &invalid }},
		{name: "missing archive checksum", mutate: func(item *domain.AppDeployment, _ *string) { item.ImageArchiveSHA256 = nil }},
		{name: "invalid archive checksum", mutate: func(item *domain.AppDeployment, _ *string) {
			invalid := strings.Repeat("g", 64)
			item.ImageArchiveSHA256 = &invalid
		}},
		{name: "missing image size", mutate: func(item *domain.AppDeployment, _ *string) { item.ImageSizeBytes = nil }},
		{name: "nonpositive image size", mutate: func(item *domain.AppDeployment, _ *string) { invalid := int64(0); item.ImageSizeBytes = &invalid }},
		{name: "missing path", mutate: func(_ *domain.AppDeployment, candidate *string) { *candidate = "" }},
		{name: "path names another deployment", mutate: func(_ *domain.AppDeployment, candidate *string) {
			*candidate = projectID.String() + "/" + appID.String() + "/" + uuid.Must(uuid.NewV7()).String()
		}},
		{name: "nested path", mutate: func(_ *domain.AppDeployment, candidate *string) { *candidate += "/extra" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := target
			candidate := path
			test.mutate(&item, &candidate)
			if appRollbackArtifactReady(item, &candidate, projectID, appID, deploymentID) {
				t.Fatalf("invalid artifact metadata was accepted: %+v path=%q", item, candidate)
			}
		})
	}
}

func TestCanonicalAppRollbackWorkloadRequiresStoredDigest(t *testing.T) {
	snapshot := workloadspec.Default()
	digest, err := workloadspec.Digest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	canonical, actualDigest, err := canonicalAppRollbackWorkload(snapshot, digest)
	if err != nil || len(canonical) == 0 || actualDigest != digest {
		t.Fatalf("valid snapshot validation = digest %q error %v", actualDigest, err)
	}
	if _, _, err := canonicalAppRollbackWorkload(snapshot, strings.Repeat("f", 64)); err == nil {
		t.Fatal("snapshot with a mismatched stored digest was accepted")
	}
}
