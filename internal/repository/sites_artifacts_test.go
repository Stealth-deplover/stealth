package repository

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSiteArtifactPathValidationRequiresThreeUUIDv7Segments(t *testing.T) {
	projectID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	siteID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	deploymentID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	valid := strings.Join([]string{projectID.String(), siteID.String(), deploymentID.String()}, "/")
	if !validSiteArtifactPath(valid) {
		t.Fatal("valid UUIDv7 artifact path was rejected")
	}
	for _, invalid := range []string{
		"../" + valid,
		"/" + valid,
		strings.Join([]string{projectID.String(), siteID.String()}, "/"),
		strings.Join([]string{uuid.New().String(), siteID.String(), deploymentID.String()}, "/"),
	} {
		if validSiteArtifactPath(invalid) {
			t.Errorf("validSiteArtifactPath(%q) = true", invalid)
		}
	}
}

func TestSiteDeploymentValidationHelpersStayBounded(t *testing.T) {
	if !validSiteSHA256(strings.Repeat("a", 64)) {
		t.Fatal("valid SHA-256 was rejected")
	}
	for _, value := range []string{"", strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		if validSiteSHA256(value) {
			t.Errorf("validSiteSHA256(%q) = true", value)
		}
	}
	for _, value := range []string{"dist", "public/assets", "."} {
		if !validSiteOutputDirectory(value) {
			t.Errorf("validSiteOutputDirectory(%q) = false", value)
		}
	}
	for _, value := range []string{"../dist", "/tmp", "public//assets", "public\\assets", "public\nassets"} {
		if validSiteOutputDirectory(value) {
			t.Errorf("validSiteOutputDirectory(%q) = true", value)
		}
	}
}
