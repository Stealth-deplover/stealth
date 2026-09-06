package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Options configures any S3-compatible object store (AWS S3, MinIO,
// Cloudflare R2, Wasabi, or another SigV4-compatible service). StagingRoot is
// local scratch space used only while streaming an upload or download.
type S3Options struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	UseSSL         bool
	ForcePathStyle bool
	Prefix         string
	StagingRoot    string
}

// S3Store publishes durable blobs to an S3-compatible bucket. A local Store
// is retained as a bounded staging area so request limits, checksums, MIME
// sniffing, and retry-safe cleanup stay identical to the local backend.
type S3Store struct {
	client  *minio.Client
	bucket  string
	prefix  string
	maxSize int64
	staging *Store
}

func NewS3(options S3Options, maxSize int64) (*S3Store, error) {
	endpoint, secure, err := normalizeEndpoint(options.Endpoint, options.UseSSL)
	if err != nil {
		return nil, err
	}
	if !validBucketName(options.Bucket) {
		return nil, fmt.Errorf("storage S3 bucket is invalid")
	}
	if strings.TrimSpace(options.AccessKey) == "" || strings.TrimSpace(options.SecretKey) == "" {
		return nil, fmt.Errorf("storage S3 access and secret keys are required")
	}
	if maxSize <= 0 {
		return nil, fmt.Errorf("storage max size must be positive")
	}
	stagingRoot := strings.TrimSpace(options.StagingRoot)
	if stagingRoot == "" {
		return nil, fmt.Errorf("storage S3 staging root is required")
	}
	staging, err := New(stagingRoot, maxSize)
	if err != nil {
		return nil, fmt.Errorf("create S3 staging store: %w", err)
	}
	lookup := minio.BucketLookupAuto
	if options.ForcePathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(options.AccessKey, options.SecretKey, ""),
		Secure:       secure,
		Region:       strings.TrimSpace(options.Region),
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}
	return &S3Store{client: client, bucket: options.Bucket, prefix: strings.Trim(options.Prefix, "/"), maxSize: maxSize, staging: staging}, nil
}

func validBucketName(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 63 || value[0] == '-' || value[0] == '.' || value[len(value)-1] == '-' || value[len(value)-1] == '.' {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' || character == '.' {
			continue
		}
		if character >= 'A' && character <= 'Z' {
			return false
		}
		return false
	}
	return !strings.Contains(value, "..") && !strings.Contains(value, ".-") && !strings.Contains(value, "-.")
}

func normalizeEndpoint(raw string, useSSL bool) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("storage S3 endpoint is required")
	}
	secure := useSSL
	endpointURL := raw
	if !strings.Contains(raw, "://") {
		endpointURL = "http://" + raw
	}
	parsed, err := url.Parse(endpointURL)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", false, fmt.Errorf("storage S3 endpoint must be an absolute HTTP(S) host without path or credentials")
	}
	if strings.Contains(raw, "://") {
		secure = parsed.Scheme == "https"
	}
	raw = parsed.Host
	if strings.ContainsAny(raw, "/?#") || strings.Contains(raw, " ") {
		return "", false, fmt.Errorf("storage S3 endpoint is invalid")
	}
	return raw, secure, nil
}

func (s *S3Store) Ping(ctx context.Context) error {
	if s == nil || s.client == nil || strings.TrimSpace(s.bucket) == "" {
		return ErrInvalidPath
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check S3 bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf("S3 bucket %q does not exist", s.bucket)
	}
	return nil
}

func (s *S3Store) BeginUploadWithLimit(ctx context.Context, projectID, bucketID, fileID uuid.UUID, src io.Reader, declaredType string, maxSize int64) (PreparedFile, error) {
	if s == nil || s.staging == nil {
		return PreparedFile{}, ErrInvalidPath
	}
	return s.staging.BeginUploadWithLimit(ctx, projectID, bucketID, fileID, src, declaredType, maxSize)
}

func (s *S3Store) Commit(file *PreparedFile) error {
	if s == nil || s.client == nil || s.staging == nil || file == nil || file.TempPath == "" || file.RelativePath == "" || file.committed {
		return ErrInvalidPath
	}
	key, err := s.objectKey(file.RelativePath)
	if err != nil {
		return err
	}
	if _, statErr := s.client.StatObject(context.Background(), s.bucket, key, minio.StatObjectOptions{}); statErr == nil {
		return fmt.Errorf("storage destination already exists: %w", os.ErrExist)
	} else if !isMissingObject(statErr) {
		return fmt.Errorf("check S3 destination: %w", statErr)
	}
	input, err := os.Open(file.TempPath)
	if err != nil {
		return fmt.Errorf("open staged upload: %w", err)
	}
	info, putErr := s.client.PutObject(context.Background(), s.bucket, key, input, file.Size, minio.PutObjectOptions{
		ContentType:  file.ContentType,
		UserMetadata: map[string]string{"checksum-sha256": file.Checksum},
	})
	closeErr := input.Close()
	if putErr != nil {
		return fmt.Errorf("publish S3 object: %w", putErr)
	}
	if info.Size != file.Size {
		_ = s.removeObject(context.Background(), key)
		return fmt.Errorf("publish S3 object: wrote %d bytes, expected %d", info.Size, file.Size)
	}
	if closeErr != nil {
		_ = s.removeObject(context.Background(), key)
		return fmt.Errorf("close staged upload: %w", closeErr)
	}
	if err := os.Remove(file.TempPath); err != nil {
		_ = s.removeObject(context.Background(), key)
		return fmt.Errorf("remove staged upload: %w", err)
	}
	file.committed = true
	return nil
}

func (s *S3Store) Cleanup(file *PreparedFile) {
	if s == nil || s.staging == nil {
		return
	}
	s.staging.Cleanup(file)
}

func (s *S3Store) RemoveRelative(relative string) error {
	key, err := s.objectKey(relative)
	if err != nil {
		return err
	}
	return s.removeObject(context.Background(), key)
}

func (s *S3Store) RemoveProject(projectID uuid.UUID) error {
	if s == nil || s.client == nil || projectID == uuid.Nil || projectID.Version() != uuid.Version(7) {
		return ErrInvalidPath
	}
	prefix, err := s.objectKey(projectID.String())
	if err != nil {
		return err
	}
	prefix += "/"
	ctx := context.Background()
	objects := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true})
	for object := range objects {
		if object.Err != nil {
			return fmt.Errorf("list S3 project objects: %w", object.Err)
		}
		if err := s.removeObject(ctx, object.Key); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3Store) OpenRelative(relative string) (ReadSeekCloser, error) {
	if s == nil || s.client == nil || s.staging == nil {
		return nil, ErrInvalidPath
	}
	key, err := s.objectKey(relative)
	if err != nil {
		return nil, err
	}
	temporary, err := os.CreateTemp(s.staging.root, ".download-*")
	if err != nil {
		return nil, fmt.Errorf("create S3 download staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("protect S3 download staging file: %w", err)
	}
	object, err := s.client.GetObject(context.Background(), s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		cleanup()
		return nil, normalizeObjectError(err)
	}
	copyLimit := s.maxSize
	if copyLimit < 1 {
		copyLimit = 1
	}
	readLimit := copyLimit
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	copied, copyErr := io.Copy(temporary, io.LimitReader(object, readLimit))
	objectCloseErr := object.Close()
	if copyErr != nil {
		cleanup()
		return nil, normalizeObjectError(copyErr)
	}
	if copied > copyLimit {
		cleanup()
		return nil, ErrTooLarge
	}
	if objectCloseErr != nil {
		cleanup()
		return nil, normalizeObjectError(objectCloseErr)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return nil, fmt.Errorf("sync S3 download staging file: %w", err)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, fmt.Errorf("rewind S3 download staging file: %w", err)
	}
	return &temporaryReadSeekCloser{File: temporary, path: temporaryPath}, nil
}

func (s *S3Store) objectKey(relative string) (string, error) {
	if s == nil || s.staging == nil {
		return "", ErrInvalidPath
	}
	if _, err := s.staging.resolveRelative(relative); err != nil {
		return "", err
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if s.prefix == "" {
		return clean, nil
	}
	return s.prefix + "/" + clean, nil
}

func (s *S3Store) removeObject(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil && !isMissingObject(err) {
		return fmt.Errorf("remove S3 object: %w", err)
	}
	return nil
}

func isMissingObject(err error) bool {
	if err == nil {
		return false
	}
	response := minio.ToErrorResponse(err)
	return response.Code == "NoSuchKey" || response.Code == "NoSuchObject" || response.StatusCode == 404
}

func normalizeObjectError(err error) error {
	if isMissingObject(err) {
		return os.ErrNotExist
	}
	return err
}

type temporaryReadSeekCloser struct {
	*os.File
	path string
}

func (f *temporaryReadSeekCloser) Close() error {
	if f == nil || f.File == nil {
		return nil
	}
	closeErr := f.File.Close()
	removeErr := os.Remove(f.path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}
