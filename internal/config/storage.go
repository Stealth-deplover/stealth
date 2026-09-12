package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// storageSettings owns the local and S3 storage environment contract. The
// application still receives one Config snapshot, but storage parsing and
// validation stay together instead of being interleaved with auth, runtime,
// and telemetry settings.
type storageSettings struct {
	root          string
	maxFileSize   int64
	defaultQuota  int64
	driver        string
	s3Endpoint    string
	s3Region      string
	s3Bucket      string
	s3AccessKey   string
	s3SecretKey   string
	s3UseSSL      bool
	s3PathStyle   bool
	s3Prefix      string
	s3StagingRoot string
}

func loadStorageSettings() (storageSettings, error) {
	root, err := filepath.Abs(value("STORAGE_ROOT", "/var/lib/stealth/storage"))
	if err != nil || strings.TrimSpace(root) == "" {
		return storageSettings{}, fmt.Errorf("STORAGE_ROOT must be a valid filesystem path")
	}
	maxFileSize, err := parseBytes(value("STORAGE_MAX_FILE_SIZE", "50MiB"))
	if err != nil || maxFileSize < 1 {
		return storageSettings{}, fmt.Errorf("STORAGE_MAX_FILE_SIZE must be a positive byte quantity")
	}
	defaultQuota, err := parseBytes(value("STORAGE_DEFAULT_QUOTA_BYTES", "1GiB"))
	if err != nil || defaultQuota < 1 {
		return storageSettings{}, fmt.Errorf("STORAGE_DEFAULT_QUOTA_BYTES must be a positive byte quantity")
	}
	driver := strings.ToLower(value("STORAGE_DRIVER", "local"))
	if driver != "local" && driver != "s3" {
		return storageSettings{}, fmt.Errorf("STORAGE_DRIVER must be local or s3")
	}
	s3Endpoint := strings.TrimSpace(os.Getenv("STORAGE_S3_ENDPOINT"))
	s3Region := value("STORAGE_S3_REGION", "us-east-1")
	s3Bucket := strings.TrimSpace(os.Getenv("STORAGE_S3_BUCKET"))
	s3AccessKey := strings.TrimSpace(os.Getenv("STORAGE_S3_ACCESS_KEY"))
	s3SecretKey := os.Getenv("STORAGE_S3_SECRET_KEY")
	s3UseSSL, err := strconv.ParseBool(value("STORAGE_S3_USE_SSL", "true"))
	if err != nil {
		return storageSettings{}, fmt.Errorf("STORAGE_S3_USE_SSL must be true or false")
	}
	s3PathStyle, err := strconv.ParseBool(value("STORAGE_S3_PATH_STYLE", "true"))
	if err != nil {
		return storageSettings{}, fmt.Errorf("STORAGE_S3_PATH_STYLE must be true or false")
	}
	s3Prefix := strings.Trim(strings.TrimSpace(os.Getenv("STORAGE_S3_PREFIX")), "/")
	if len(s3Prefix) > 512 || strings.ContainsAny(s3Prefix, "\\\r\n\x00") {
		return storageSettings{}, fmt.Errorf("STORAGE_S3_PREFIX must be a safe object prefix")
	}
	if driver == "s3" {
		if s3Endpoint == "" || s3Bucket == "" || s3AccessKey == "" || s3SecretKey == "" {
			return storageSettings{}, fmt.Errorf("STORAGE_S3_ENDPOINT, STORAGE_S3_BUCKET, STORAGE_S3_ACCESS_KEY, and STORAGE_S3_SECRET_KEY are required when STORAGE_DRIVER is s3")
		}
		if !isStorageS3Endpoint(s3Endpoint) {
			return storageSettings{}, fmt.Errorf("STORAGE_S3_ENDPOINT must be an HTTP(S) endpoint without path or credentials")
		}
	}
	s3StagingRoot, err := filepath.Abs(value("STORAGE_S3_STAGING_ROOT", filepath.Join(root, "s3-staging")))
	if err != nil || strings.TrimSpace(s3StagingRoot) == "" || s3StagingRoot == string(filepath.Separator) {
		return storageSettings{}, fmt.Errorf("STORAGE_S3_STAGING_ROOT must be a valid non-root filesystem path")
	}

	return storageSettings{
		root:          root,
		maxFileSize:   maxFileSize,
		defaultQuota:  defaultQuota,
		driver:        driver,
		s3Endpoint:    s3Endpoint,
		s3Region:      s3Region,
		s3Bucket:      s3Bucket,
		s3AccessKey:   s3AccessKey,
		s3SecretKey:   s3SecretKey,
		s3UseSSL:      s3UseSSL,
		s3PathStyle:   s3PathStyle,
		s3Prefix:      s3Prefix,
		s3StagingRoot: s3StagingRoot,
	}, nil
}

func (s storageSettings) apply(c *Config) {
	c.StorageRoot = filepath.Clean(s.root)
	c.StorageMaxFileSize = s.maxFileSize
	c.StorageDefaultQuotaBytes = s.defaultQuota
	c.StorageDriver = s.driver
	c.StorageS3Endpoint = s.s3Endpoint
	c.StorageS3Region = s.s3Region
	c.StorageS3Bucket = s.s3Bucket
	c.StorageS3AccessKey = s.s3AccessKey
	c.StorageS3SecretKey = s.s3SecretKey
	c.StorageS3UseSSL = s.s3UseSSL
	c.StorageS3PathStyle = s.s3PathStyle
	c.StorageS3Prefix = s.s3Prefix
	c.StorageS3StagingRoot = filepath.Clean(s.s3StagingRoot)
}

func (c *Config) applyStorageDefaults() {
	if c.StorageRoot == "" {
		c.StorageRoot = "/var/lib/stealth/storage"
	}
	if c.StorageMaxFileSize <= 0 {
		c.StorageMaxFileSize = 50 << 20
	}
	if c.StorageDefaultQuotaBytes <= 0 {
		c.StorageDefaultQuotaBytes = 1 << 30
	}
}

func (c Config) ValidateStorage() error {
	if c.StorageDriver != "local" && c.StorageDriver != "s3" {
		return fmt.Errorf("storage driver must be local or s3")
	}
	if c.StorageMaxFileSize <= 0 || c.StorageDefaultQuotaBytes <= 0 {
		return fmt.Errorf("storage size and quota settings are invalid")
	}
	if c.StorageDriver == "s3" {
		if strings.TrimSpace(c.StorageS3Endpoint) == "" || strings.TrimSpace(c.StorageS3Bucket) == "" || strings.TrimSpace(c.StorageS3AccessKey) == "" || c.StorageS3SecretKey == "" {
			return fmt.Errorf("S3 storage credentials and bucket are required")
		}
		if !isStorageS3Endpoint(c.StorageS3Endpoint) {
			return fmt.Errorf("S3 storage endpoint is invalid")
		}
		if strings.TrimSpace(c.StorageS3StagingRoot) == "" || !filepath.IsAbs(c.StorageS3StagingRoot) || filepath.Clean(c.StorageS3StagingRoot) == string(filepath.Separator) {
			return fmt.Errorf("S3 storage staging root is invalid")
		}
	}
	return nil
}
