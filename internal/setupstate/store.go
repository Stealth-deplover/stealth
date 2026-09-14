// Package setupstate contains the durable, server-owned state for the
// pre-production browser setup flow. It intentionally separates public draft
// choices from encrypted provider credentials.
package setupstate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
)

const (
	PhaseCollecting    = "collecting"
	PhaseInstalling    = "installing"
	PhaseComplete      = "complete"
	PhaseFailed        = "failed"
	PhaseHandoff       = "handoff"
	stateVersion       = 2
	legacyStateVersion = 1
)

// Draft contains setup choices that can safely be represented by the public
// setup-state projection. Credential material belongs in SetupCredentials.
type Draft struct {
	InstanceName       string `json:"instance_name,omitempty"`
	PublicURL          string `json:"public_url,omitempty"`
	NetworkMode        string `json:"network_mode,omitempty"`
	Hostname           string `json:"hostname,omitempty"`
	DatabaseMode       string `json:"database_mode,omitempty"`
	DatabaseTested     bool   `json:"database_tested,omitempty"`
	RedisMode          string `json:"redis_mode,omitempty"`
	RedisTested        bool   `json:"redis_tested,omitempty"`
	StorageMode        string `json:"storage_mode,omitempty"`
	StorageTested      bool   `json:"storage_tested,omitempty"`
	StorageS3Endpoint  string `json:"storage_s3_endpoint,omitempty"`
	StorageS3Region    string `json:"storage_s3_region,omitempty"`
	StorageS3Bucket    string `json:"storage_s3_bucket,omitempty"`
	StorageS3UseSSL    bool   `json:"storage_s3_use_ssl"`
	StorageS3PathStyle bool   `json:"storage_s3_path_style"`
	StorageS3Prefix    string `json:"storage_s3_prefix,omitempty"`
}

// SetupCredentials is the single in-memory view of setup credentials. The
// FileStore persists these values only through State.Secrets.
type SetupCredentials struct {
	DatabaseURL        string
	RedisURL           string
	StorageS3AccessKey string
	StorageS3SecretKey string
}

type GitHubState struct {
	Mode                   string    `json:"mode,omitempty"`
	ClientID               string    `json:"client_id,omitempty"`
	ManifestStateHash      string    `json:"manifest_state_hash,omitempty"`
	ManifestExpiresAt      time.Time `json:"manifest_expires_at,omitempty"`
	AuthorizationStateHash string    `json:"authorization_state_hash,omitempty"`
	AuthorizationExpiresAt time.Time `json:"authorization_expires_at,omitempty"`
	Connected              bool      `json:"connected,omitempty"`
	AuthorizationSession   string    `json:"authorization_session,omitempty"`
}

type CloudflareState struct {
	Mode           string            `json:"mode,omitempty"`
	Connected      bool              `json:"connected,omitempty"`
	ExpiresAt      time.Time         `json:"expires_at,omitempty"`
	TokenValid     bool              `json:"token_valid,omitempty"`
	Binding        CloudflareBinding `json:"binding,omitempty"`
	OAuthStateHash string            `json:"oauth_state_hash,omitempty"`
	OAuthExpiresAt time.Time         `json:"oauth_expires_at,omitempty"`
}

// State is encrypted in its entirety when persisted. Secrets are available to
// the setup module through methods, but the HTTP adapter only serializes the
// public projection below.
type State struct {
	Version        int               `json:"version"`
	Phase          string            `json:"phase"`
	Step           string            `json:"step,omitempty"`
	ErrorCode      string            `json:"error_code,omitempty"`
	ErrorMessage   string            `json:"error_message,omitempty"`
	Draft          Draft             `json:"draft"`
	GitHub         GitHubState       `json:"github"`
	Cloudflare     CloudflareState   `json:"cloudflare"`
	Secrets        map[string]string `json:"secrets,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at"`
	QuickTunnel    string            `json:"quick_tunnel,omitempty"`
	SetupSessionID string            `json:"setup_session_id,omitempty"`
	SetupCodeHash  string            `json:"setup_code_hash,omitempty"`
	SetupExpiresAt time.Time         `json:"setup_expires_at,omitempty"`
	InstallRunID   string            `json:"install_run_id,omitempty"`
	LastEventID    uint64            `json:"last_event_id,omitempty"`
}

const (
	setupDatabaseURLSecret        = "database_url"
	setupRedisURLSecret           = "redis_url"
	setupStorageS3AccessKeySecret = "storage_s3_access_key"
	setupStorageS3SecretKeySecret = "storage_s3_secret_key"
)

func (s State) SetupCredentials() SetupCredentials {
	return SetupCredentials{
		DatabaseURL:        s.Secret(setupDatabaseURLSecret),
		RedisURL:           s.Secret(setupRedisURLSecret),
		StorageS3AccessKey: s.Secret(setupStorageS3AccessKeySecret),
		StorageS3SecretKey: s.Secret(setupStorageS3SecretKeySecret),
	}
}

func (s *State) SetSetupCredentials(credentials SetupCredentials) {
	s.SetSecret(setupDatabaseURLSecret, credentials.DatabaseURL)
	s.SetSecret(setupRedisURLSecret, credentials.RedisURL)
	s.SetSecret(setupStorageS3AccessKeySecret, credentials.StorageS3AccessKey)
	s.SetSecret(setupStorageS3SecretKeySecret, credentials.StorageS3SecretKey)
}

type PublicState struct {
	Version      int              `json:"version"`
	Phase        string           `json:"phase"`
	Step         string           `json:"step,omitempty"`
	ErrorCode    string           `json:"error_code,omitempty"`
	ErrorMessage string           `json:"error_message,omitempty"`
	Draft        PublicDraft      `json:"draft"`
	GitHub       PublicGitHub     `json:"github"`
	Cloudflare   PublicCloudflare `json:"cloudflare"`
	UpdatedAt    time.Time        `json:"updated_at"`
	LastEventID  uint64           `json:"last_event_id,omitempty"`
}

type PublicDraft struct {
	InstanceName         string `json:"instance_name,omitempty"`
	PublicURL            string `json:"public_url,omitempty"`
	NetworkMode          string `json:"network_mode,omitempty"`
	Hostname             string `json:"hostname,omitempty"`
	CloudflareAccountID  string `json:"cloudflare_account_id,omitempty"`
	CloudflareZoneID     string `json:"cloudflare_zone_id,omitempty"`
	CloudflareTunnelID   string `json:"cloudflare_tunnel_id,omitempty"`
	CloudflareTunnelName string `json:"cloudflare_tunnel_name,omitempty"`
	CloudflareRecordID   string `json:"cloudflare_record_id,omitempty"`
	DatabaseMode         string `json:"database_mode,omitempty"`
	DatabaseTested       bool   `json:"database_tested,omitempty"`
	RedisMode            string `json:"redis_mode,omitempty"`
	RedisTested          bool   `json:"redis_tested,omitempty"`
	StorageMode          string `json:"storage_mode,omitempty"`
	StorageTested        bool   `json:"storage_tested,omitempty"`
	StorageS3Endpoint    string `json:"storage_s3_endpoint,omitempty"`
	StorageS3Region      string `json:"storage_s3_region,omitempty"`
	StorageS3Bucket      string `json:"storage_s3_bucket,omitempty"`
	StorageS3UseSSL      bool   `json:"storage_s3_use_ssl"`
	StorageS3PathStyle   bool   `json:"storage_s3_path_style"`
	StorageS3Prefix      string `json:"storage_s3_prefix,omitempty"`
}

type PublicGitHub struct {
	Mode                 string    `json:"mode,omitempty"`
	ClientID             string    `json:"client_id,omitempty"`
	Connected            bool      `json:"connected,omitempty"`
	AuthorizationSession string    `json:"authorization_session,omitempty"`
	ManifestExpiresAt    time.Time `json:"manifest_expires_at,omitempty"`
}

type PublicCloudflare struct {
	Mode       string    `json:"mode,omitempty"`
	Connected  bool      `json:"connected,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	TokenValid bool      `json:"token_valid,omitempty"`
}

func NewState() State {
	return State{Version: stateVersion, Phase: PhaseCollecting, Draft: Draft{NetworkMode: "cloudflare_tunnel", DatabaseMode: "bundled", RedisMode: "bundled", StorageMode: "local"}, Secrets: make(map[string]string), UpdatedAt: time.Now().UTC()}
}

func (s State) Public() PublicState {
	binding := s.EffectiveCloudflareBinding()
	return PublicState{
		Version:      s.Version,
		Phase:        s.Phase,
		Step:         s.Step,
		ErrorCode:    s.ErrorCode,
		ErrorMessage: s.ErrorMessage,
		Draft: PublicDraft{
			InstanceName: s.Draft.InstanceName, PublicURL: s.Draft.PublicURL,
			NetworkMode: s.Draft.NetworkMode, Hostname: s.Draft.Hostname,
			CloudflareAccountID: binding.AccountID, CloudflareZoneID: binding.ZoneID,
			CloudflareTunnelID: binding.TunnelID, CloudflareTunnelName: binding.TunnelName, CloudflareRecordID: binding.RecordID,
			DatabaseMode: s.Draft.DatabaseMode, DatabaseTested: s.Draft.DatabaseTested,
			RedisMode: s.Draft.RedisMode, RedisTested: s.Draft.RedisTested,
			StorageMode: s.Draft.StorageMode, StorageTested: s.Draft.StorageTested,
			StorageS3Endpoint: s.Draft.StorageS3Endpoint, StorageS3Region: s.Draft.StorageS3Region,
			StorageS3Bucket: s.Draft.StorageS3Bucket, StorageS3UseSSL: s.Draft.StorageS3UseSSL,
			StorageS3PathStyle: s.Draft.StorageS3PathStyle, StorageS3Prefix: s.Draft.StorageS3Prefix,
		},
		GitHub:      PublicGitHub{Mode: s.GitHub.Mode, ClientID: s.GitHub.ClientID, Connected: s.GitHub.Connected, AuthorizationSession: s.GitHub.AuthorizationSession, ManifestExpiresAt: s.GitHub.ManifestExpiresAt},
		Cloudflare:  PublicCloudflare{Mode: s.Cloudflare.Mode, Connected: s.Cloudflare.Connected, ExpiresAt: s.Cloudflare.ExpiresAt, TokenValid: s.Cloudflare.TokenValid},
		UpdatedAt:   s.UpdatedAt,
		LastEventID: s.LastEventID,
	}
}

func (s State) Secret(name string) string {
	return s.Secrets[strings.TrimSpace(name)]
}

func (s *State) SetSecret(name, value string) {
	if s.Secrets == nil {
		s.Secrets = make(map[string]string)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if strings.TrimSpace(value) == "" {
		delete(s.Secrets, name)
		return
	}
	s.Secrets[name] = value
}

type Store interface {
	Load(context.Context) (State, error)
	Save(context.Context, State) error
	Update(context.Context, func(*State) error) (State, error)
}

var ErrUnavailable = errors.New("setup state is unavailable")

type FileStore struct {
	path   string
	cipher *functionsecret.Cipher
	mu     sync.Mutex
}

// legacyDraft contains the credential fields written by state version 1. It
// is deliberately separate from Draft so new state cannot accidentally gain a
// second credential source again.
type legacyDraft struct {
	DatabaseURL          string `json:"database_url,omitempty"`
	RedisURL             string `json:"redis_url,omitempty"`
	StorageS3AccessKey   string `json:"storage_s3_access_key,omitempty"`
	StorageS3SecretKey   string `json:"storage_s3_secret_key,omitempty"`
	CloudflareAccountID  string `json:"cloudflare_account_id,omitempty"`
	CloudflareZoneID     string `json:"cloudflare_zone_id,omitempty"`
	CloudflareTunnelID   string `json:"cloudflare_tunnel_id,omitempty"`
	CloudflareTunnelName string `json:"cloudflare_tunnel_name,omitempty"`
	CloudflareRecordID   string `json:"cloudflare_record_id,omitempty"`
}

type legacyState struct {
	Draft legacyDraft `json:"draft"`
}

func NewFileStore(path string, cipher *functionsecret.Cipher) (*FileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" || filepath.Clean(path) == string(filepath.Separator) || cipher == nil {
		return nil, ErrUnavailable
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	clean := filepath.Clean(abs)
	if clean == string(filepath.Separator) {
		return nil, ErrUnavailable
	}
	return &FileStore{path: clean, cipher: cipher}, nil
}

func (s *FileStore) Load(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(ctx)
}

func (s *FileStore) Save(ctx context.Context, state State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state.UpdatedAt = time.Now().UTC()
	state.Version = stateVersion
	if err := ValidateState(state); err != nil {
		return err
	}
	return s.saveLocked(state)
}

// Update serializes the complete read-modify-write cycle. Setup requests and
// the asynchronous installer use it so two browser tabs cannot overwrite a
// newer provider connection or install phase with an older snapshot.
func (s *FileStore) Update(ctx context.Context, mutate func(*State) error) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	if mutate == nil {
		return State{}, errors.New("setup state update function is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked(ctx)
	if err != nil {
		return State{}, err
	}
	if err := mutate(&state); err != nil {
		return State{}, err
	}
	state.UpdatedAt = time.Now().UTC()
	state.Version = stateVersion
	if err := ValidateState(state); err != nil {
		return State{}, err
	}
	if err := s.saveLocked(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *FileStore) loadLocked(ctx context.Context) (State, error) {
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	contents, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read setup state: %w", err)
	}
	plaintext, err := s.cipher.Decrypt(contents)
	if err != nil {
		return State{}, fmt.Errorf("decrypt setup state: %w", err)
	}
	var state State
	if err := json.Unmarshal(plaintext, &state); err != nil {
		return State{}, fmt.Errorf("decode setup state: %w", err)
	}
	var legacy legacyState
	if err := json.Unmarshal(plaintext, &legacy); err != nil {
		return State{}, fmt.Errorf("decode legacy setup state: %w", err)
	}
	if err := migrateState(&state, legacy); err != nil {
		return State{}, err
	}
	if err := ValidateState(state); err != nil {
		return State{}, err
	}
	if state.Secrets == nil {
		state.Secrets = make(map[string]string)
	}
	return state, nil
}

func migrateState(state *State, legacy legacyState) error {
	if state.Version != 0 && state.Version != legacyStateVersion && state.Version != stateVersion {
		return fmt.Errorf("unsupported setup state version")
	}
	credentials := state.SetupCredentials()
	if credentials.DatabaseURL == "" {
		credentials.DatabaseURL = legacy.Draft.DatabaseURL
	}
	if credentials.RedisURL == "" {
		credentials.RedisURL = legacy.Draft.RedisURL
	}
	if credentials.StorageS3AccessKey == "" {
		credentials.StorageS3AccessKey = legacy.Draft.StorageS3AccessKey
	}
	if credentials.StorageS3SecretKey == "" {
		credentials.StorageS3SecretKey = legacy.Draft.StorageS3SecretKey
	}
	state.SetSetupCredentials(credentials)
	if state.Cloudflare.Binding.IsZero() {
		binding := cloudflareBindingFromLegacyDraft(legacy.Draft)
		binding.Hostname = canonicalHostname(state.Draft.Hostname)
		if binding.HasIntent() {
			state.Cloudflare.Binding = binding
		}
	}
	state.Version = stateVersion
	return nil
}

func (s *FileStore) saveLocked(state State) error {
	if state.Secrets == nil {
		state.Secrets = make(map[string]string)
	}
	plaintext, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode setup state: %w", err)
	}
	ciphertext, err := s.cipher.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypt setup state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create setup state directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".setup-state-*")
	if err != nil {
		return fmt.Errorf("create setup state file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(ciphertext); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write setup state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync setup state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, s.path); err != nil {
		return fmt.Errorf("commit setup state: %w", err)
	}
	return os.Chmod(s.path, 0o600)
}

func NewCallbackState(purpose string) (plain string, hash string, expiresAt time.Time, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", time.Time{}, fmt.Errorf("generate %s state: %w", purpose, err)
	}
	plain = base64.RawURLEncoding.EncodeToString(bytes)
	// The raw state is returned only to the redirect URL. Callers should persist
	// the digest, which keeps a database/file dump from becoming a callback
	// credential.
	hashBytes := sha256Bytes([]byte(plain))
	hash = base64.RawURLEncoding.EncodeToString(hashBytes)
	return plain, hash, time.Now().UTC().Add(10 * time.Minute), nil
}

func NewManifestState() (plain string, hash string, expiresAt time.Time, err error) {
	return NewCallbackState("GitHub manifest")
}

func NewOAuthState() (plain string, hash string, expiresAt time.Time, err error) {
	return NewCallbackState("GitHub OAuth")
}

func HashCallbackState(value string) string {
	return base64.RawURLEncoding.EncodeToString(sha256Bytes([]byte(strings.TrimSpace(value))))
}

func HashManifestState(value string) string {
	return HashCallbackState(value)
}

func HashOAuthState(value string) string {
	return HashCallbackState(value)
}

func ValidateState(state State) error {
	if state.Version != 0 && state.Version != stateVersion {
		return fmt.Errorf("unsupported setup state version")
	}
	if state.Phase == "" {
		return errors.New("setup state phase is required")
	}
	switch state.Phase {
	case PhaseCollecting, PhaseInstalling, PhaseComplete, PhaseFailed, PhaseHandoff:
	default:
		return fmt.Errorf("unsupported setup state phase")
	}
	if len(state.ErrorMessage) > 240 || strings.ContainsAny(state.ErrorMessage, "\x00\r\n") {
		return errors.New("setup state error is invalid")
	}
	if len(state.Step) > 120 || strings.ContainsAny(state.Step, "\x00\r\n") {
		return errors.New("setup state step is invalid")
	}
	if err := state.Cloudflare.Binding.Validate(); err != nil {
		return err
	}
	if len(state.SetupSessionID) > 64 || strings.ContainsAny(state.SetupSessionID, "\x00\r\n") || len(state.SetupCodeHash) > 128 || strings.ContainsAny(state.SetupCodeHash, "\x00\r\n") {
		return errors.New("setup bootstrap claim is invalid")
	}
	if len(state.Secrets) > 64 {
		return errors.New("setup state contains too many secrets")
	}
	for name, value := range state.Secrets {
		// Secret values are encrypted before persistence and are never copied
		// into env files or public responses. GitHub PEM private keys are
		// intentionally multi-line, so only NUL is forbidden here.
		if name == "" || len(name) > 120 || strings.ContainsAny(name, "\x00\r\n") || len(value) > 128<<10 || strings.ContainsRune(value, '\x00') {
			return errors.New("setup state secret is invalid")
		}
	}
	return nil
}

func ValidatePublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("public URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("public URL must be an absolute HTTP(S) URL without credentials, query, or fragment")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func ValidateHostname(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "\x00\r\n/:?#[\\]") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", errors.New("hostname is invalid")
	}
	if net.ParseIP(host) != nil || strings.Contains(host, "*") {
		return "", errors.New("hostname must be a DNS name, not an IP address or wildcard")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", errors.New("hostname must include a DNS zone")
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("hostname label is invalid")
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			if unicode.IsLetter(character) {
				return "", errors.New("hostname must use ASCII DNS labels")
			}
			return "", errors.New("hostname label is invalid")
		}
	}
	return host, nil
}

func ZoneNameForHostname(host string) string {
	parts := strings.Split(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), "."), ".")
	if len(parts) < 2 {
		return strings.Join(parts, ".")
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func sha256Bytes(value []byte) []byte {
	// Kept in a helper so state hashing has one implementation and no caller
	// accidentally compares the raw callback state.
	digest := sha256.Sum256(value)
	return digest[:]
}
