// Package setupconfig owns the configuration module for the browser setup
// workflow. It translates transport input into validated, durable setupstate.
package setupconfig

import (
	"errors"
	"net/url"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/githubauth"
	"github.com/Stealth-deplover/stealth/internal/setupstate"
)

// Request is the browser setup configuration input. Credential fields are
// accepted at this seam and are immediately moved into encrypted setupstate.
type Request struct {
	InstanceName       string `json:"instance_name"`
	PublicURL          string `json:"public_url"`
	NetworkMode        string `json:"network_mode"`
	Hostname           string `json:"hostname"`
	DatabaseMode       string `json:"database_mode"`
	DatabaseURL        string `json:"database_url"`
	RedisMode          string `json:"redis_mode"`
	RedisURL           string `json:"redis_url"`
	StorageMode        string `json:"storage_mode"`
	StorageS3Endpoint  string `json:"storage_s3_endpoint"`
	StorageS3Region    string `json:"storage_s3_region"`
	StorageS3Bucket    string `json:"storage_s3_bucket"`
	StorageS3AccessKey string `json:"storage_s3_access_key"`
	StorageS3SecretKey string `json:"storage_s3_secret_key"`
	StorageS3UseSSL    *bool  `json:"storage_s3_use_ssl"`
	StorageS3PathStyle *bool  `json:"storage_s3_path_style"`
	StorageS3Prefix    string `json:"storage_s3_prefix"`
}

// Apply validates and applies one configuration request to state. The caller
// should invoke it inside setupstate.Store.Update so the complete change is
// serialized with provider and installation state transitions.
func Apply(state *setupstate.State, request Request) error {
	if state == nil {
		return errors.New("setup state is required")
	}
	instanceName := strings.TrimSpace(request.InstanceName)
	if instanceName == "" {
		instanceName = "Stealth"
	}
	if len(instanceName) > 120 || strings.ContainsAny(instanceName, "\x00\r\n") {
		return errors.New("instance name is invalid")
	}
	if state.Phase == setupstate.PhaseInstalling || state.Phase == setupstate.PhaseHandoff || state.Phase == setupstate.PhaseComplete {
		return errors.New("installation is already in progress or complete")
	}

	credentials := state.SetupCredentials()
	networkMode := strings.ToLower(strings.TrimSpace(request.NetworkMode))
	if networkMode == "" {
		networkMode = state.Draft.NetworkMode
	}
	if !ValidNetworkMode(networkMode) {
		return errors.New("network mode is invalid")
	}

	hostname := strings.TrimSpace(request.Hostname)
	if hostname != "" {
		validatedHostname, err := setupstate.ValidateHostname(hostname)
		if err != nil {
			return err
		}
		hostname = validatedHostname
	}
	publicURL := strings.TrimSpace(request.PublicURL)
	if publicURL == "" && hostname != "" {
		publicURL = "https://" + hostname
	}
	if publicURL == "" {
		publicURL = "http://localhost:8081"
	}
	validatedPublicURL, err := setupstate.ValidatePublicURL(publicURL)
	if err != nil {
		return err
	}
	publicURL = validatedPublicURL

	databaseMode := strings.ToLower(strings.TrimSpace(request.DatabaseMode))
	if databaseMode == "" {
		databaseMode = "bundled"
	}
	if databaseMode != "bundled" && databaseMode != "external" {
		return errors.New("database mode must be bundled or external")
	}
	databaseURL := strings.TrimSpace(request.DatabaseURL)
	if databaseMode == "external" {
		if databaseURL == "" {
			databaseURL = credentials.DatabaseURL
		}
		if !ValidDatabaseURL(databaseURL) {
			return errors.New("external PostgreSQL URL is invalid")
		}
	} else {
		databaseURL = ""
	}

	redisMode := strings.ToLower(strings.TrimSpace(request.RedisMode))
	if redisMode == "" {
		redisMode = "bundled"
	}
	if redisMode != "bundled" && redisMode != "external" {
		return errors.New("Redis mode must be bundled or external")
	}
	redisURL := strings.TrimSpace(request.RedisURL)
	if redisMode == "external" {
		if redisURL == "" {
			redisURL = credentials.RedisURL
		}
		if !ValidRedisURL(redisURL) {
			return errors.New("external Redis URL is invalid")
		}
	} else {
		redisURL = ""
	}

	storageMode := strings.ToLower(strings.TrimSpace(request.StorageMode))
	if storageMode == "" {
		storageMode = "local"
	}
	if storageMode != "local" && storageMode != "s3" {
		return errors.New("storage mode must be local or s3")
	}
	storageEndpoint := strings.TrimSpace(request.StorageS3Endpoint)
	if storageEndpoint == "" {
		storageEndpoint = state.Draft.StorageS3Endpoint
	}
	storageRegion := strings.TrimSpace(request.StorageS3Region)
	if storageRegion == "" {
		storageRegion = state.Draft.StorageS3Region
	}
	storageBucket := strings.TrimSpace(request.StorageS3Bucket)
	if storageBucket == "" {
		storageBucket = state.Draft.StorageS3Bucket
	}
	storageAccessKey := strings.TrimSpace(request.StorageS3AccessKey)
	if storageAccessKey == "" {
		storageAccessKey = credentials.StorageS3AccessKey
	}
	storageSecretKey := request.StorageS3SecretKey
	if storageSecretKey == "" {
		storageSecretKey = credentials.StorageS3SecretKey
	}
	storageUseSSL := state.Draft.StorageS3UseSSL
	if request.StorageS3UseSSL != nil {
		storageUseSSL = *request.StorageS3UseSSL
	}
	storagePathStyle := state.Draft.StorageS3PathStyle
	if request.StorageS3PathStyle != nil {
		storagePathStyle = *request.StorageS3PathStyle
	}
	if storageMode == "s3" && !ValidS3Settings(storageEndpoint, storageRegion, storageBucket, storageAccessKey, storageSecretKey) {
		return errors.New("S3-compatible storage settings are incomplete or invalid")
	}
	if err := validateCloudflareMutation(*state, networkMode, hostname); err != nil {
		return err
	}

	databaseChanged := state.Draft.DatabaseMode != databaseMode || credentials.DatabaseURL != databaseURL
	redisChanged := state.Draft.RedisMode != redisMode || credentials.RedisURL != redisURL
	storagePrefix := strings.Trim(strings.TrimSpace(request.StorageS3Prefix), "/")
	storageChanged := state.Draft.StorageMode != storageMode ||
		state.Draft.StorageS3Endpoint != storageEndpoint ||
		state.Draft.StorageS3Region != storageRegion ||
		state.Draft.StorageS3Bucket != storageBucket ||
		credentials.StorageS3AccessKey != storageAccessKey ||
		credentials.StorageS3SecretKey != storageSecretKey ||
		state.Draft.StorageS3UseSSL != storageUseSSL ||
		state.Draft.StorageS3PathStyle != storagePathStyle ||
		state.Draft.StorageS3Prefix != storagePrefix

	state.Draft.InstanceName = instanceName
	state.Draft.PublicURL = publicURL
	state.Draft.NetworkMode = networkMode
	state.Draft.Hostname = hostname
	state.Draft.DatabaseMode = databaseMode
	state.Draft.RedisMode = redisMode
	state.Draft.StorageMode = storageMode
	state.Draft.StorageS3Endpoint = storageEndpoint
	state.Draft.StorageS3Region = storageRegion
	state.Draft.StorageS3Bucket = storageBucket
	state.Draft.StorageS3UseSSL = storageUseSSL
	state.Draft.StorageS3PathStyle = storagePathStyle
	state.Draft.StorageS3Prefix = storagePrefix
	if databaseChanged {
		state.Draft.DatabaseTested = false
	}
	if redisChanged {
		state.Draft.RedisTested = false
	}
	if storageChanged {
		state.Draft.StorageTested = false
	}
	state.SetSetupCredentials(setupstate.SetupCredentials{
		DatabaseURL:        databaseURL,
		RedisURL:           redisURL,
		StorageS3AccessKey: storageAccessKey,
		StorageS3SecretKey: storageSecretKey,
	})
	state.ErrorCode = ""
	state.ErrorMessage = ""
	return nil
}

func validateCloudflareMutation(state setupstate.State, networkMode, hostname string) error {
	binding := state.EffectiveCloudflareBinding()
	if !binding.HasIntent() {
		return nil
	}
	if networkMode != "cloudflare_tunnel" {
		return &setupstate.CloudflareBindingConflict{Existing: binding, Field: "network"}
	}
	// Account and zone are selected by the Cloudflare provisioning request, not
	// by this general config endpoint. Keep them fixed here and compare the
	// hostname through the same canonical binding helper used by provisioning.
	return binding.ValidateRequest(setupstate.CloudflareBinding{
		AccountID:  binding.AccountID,
		ZoneID:     binding.ZoneID,
		Hostname:   hostname,
		TunnelName: binding.TunnelName,
	})
}

// ValidNetworkMode reports whether value is a supported production ingress.
func ValidNetworkMode(value string) bool {
	switch value {
	case "cloudflare_tunnel", "public_ip", "reverse_proxy", "local_only":
		return true
	default:
		return false
	}
}

// ValidDatabaseURL reports whether raw is a PostgreSQL connection URL.
func ValidDatabaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") && parsed.Host != "" && parsed.User != nil && !strings.ContainsAny(raw, "\x00\r\n")
}

// ValidRedisURL reports whether raw is a Redis connection URL.
func ValidRedisURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "redis" || parsed.Scheme == "rediss") && parsed.Host != "" && !strings.ContainsAny(raw, "\x00\r\n")
}

// ValidS3Settings reports whether the required S3-compatible inputs are safe
// and complete enough for the live storage check.
func ValidS3Settings(endpoint, region, bucket, accessKey, secretKey string) bool {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && strings.TrimSpace(region) != "" && len(bucket) >= 3 && len(bucket) <= 63 && strings.TrimSpace(accessKey) != "" && strings.TrimSpace(secretKey) != "" && !strings.ContainsAny(endpoint+region+bucket+accessKey+secretKey, "\x00\r\n")
}

// ValidateInstallableSetup checks the durable state immediately before the
// setup installer receives it.
func ValidateInstallableSetup(state setupstate.State) error {
	credentials := state.SetupCredentials()
	if !state.GitHub.Connected || !githubauth.ValidClientID(state.GitHub.ClientID) {
		return errors.New("connect GitHub before installing Stealth")
	}
	if _, err := setupstate.ValidatePublicURL(state.Draft.PublicURL); err != nil {
		return errors.New("choose a valid public Console URL")
	}
	if !ValidNetworkMode(state.Draft.NetworkMode) {
		return errors.New("choose a valid networking mode")
	}
	if state.Draft.NetworkMode == "cloudflare_tunnel" {
		if state.Cloudflare.Mode != "api_token" || !state.Cloudflare.Connected || !state.Cloudflare.TokenValid {
			return errors.New("verify a scoped Cloudflare API token before installing")
		}
		binding := state.EffectiveCloudflareBinding()
		if err := state.Cloudflare.Binding.ValidateDraft(state.Draft); err != nil {
			return err
		}
		if binding.Hostname == "" || binding.AccountID == "" || binding.ZoneID == "" || binding.TunnelID == "" || binding.RecordID == "" || state.Secret("cloudflare_access_token") == "" || state.Secret("cloudflare_tunnel_token") == "" {
			return errors.New("finish Cloudflare tunnel and DNS setup before installing")
		}
	}
	if state.Draft.DatabaseMode == "external" {
		if !ValidDatabaseURL(credentials.DatabaseURL) || !state.Draft.DatabaseTested {
			return errors.New("test and save an external PostgreSQL connection before installing")
		}
	}
	if state.Draft.RedisMode == "external" {
		if !ValidRedisURL(credentials.RedisURL) || !state.Draft.RedisTested {
			return errors.New("test and save an external Redis connection before installing")
		}
	}
	if state.Draft.StorageMode == "s3" {
		if !ValidS3Settings(state.Draft.StorageS3Endpoint, state.Draft.StorageS3Region, state.Draft.StorageS3Bucket, credentials.StorageS3AccessKey, credentials.StorageS3SecretKey) || !state.Draft.StorageTested {
			return errors.New("test and save S3-compatible storage before installing")
		}
	}
	return nil
}
