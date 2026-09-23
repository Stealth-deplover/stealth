package domain

import (
	"encoding/json"
	"time"

	"github.com/Stealth-deplover/stealth/internal/workloadspec"
)

type Account struct {
	ID            string `json:"id"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"email_verified"`
	// InstanceRole is intentionally separate from organization membership. An
	// instance owner is not implicitly a member of every organization.
	InstanceRole   string    `json:"instance_role,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	ProviderUserID string    `json:"provider_user_id,omitempty"`
	ProviderLogin  string    `json:"provider_login,omitempty"`
	DisplayName    string    `json:"display_name,omitempty"`
	AvatarURL      string    `json:"avatar_url,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// ConsoleSession is the safe, non-secret projection of a Console session.
// The bearer token and its hash are never returned to callers.
type ConsoleSession struct {
	ID        string    `json:"id"`
	IsCurrent bool      `json:"is_current"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// OrganizationPlan is the safe billing projection exposed to Console
// operators. Commercial plan definitions are server-owned; tenant responses
// contain the effective limits and current counts but never provider secrets
// or payment identifiers.
type OrganizationPlan struct {
	OrganizationID     string                 `json:"organization_id"`
	PlanKey            string                 `json:"plan_key"`
	Status             string                 `json:"status"`
	CurrentPeriodStart string                 `json:"current_period_start"`
	CurrentPeriodEnd   string                 `json:"current_period_end"`
	Limits             OrganizationPlanLimits `json:"limits"`
	Usage              OrganizationPlanUsage  `json:"usage"`
}

type OrganizationPlanLimits struct {
	Projects       int64 `json:"projects"`
	Members        int64 `json:"members"`
	Databases      int64 `json:"databases"`
	StorageBuckets int64 `json:"storage_buckets"`
	Functions      int64 `json:"functions"`
	Sites          int64 `json:"sites"`
	Apps           int64 `json:"apps"`
}

type OrganizationPlanUsage struct {
	Projects       int64 `json:"projects"`
	Members        int64 `json:"members"`
	Databases      int64 `json:"databases"`
	StorageBuckets int64 `json:"storage_buckets"`
	Functions      int64 `json:"functions"`
	Sites          int64 `json:"sites"`
	Apps           int64 `json:"apps"`
}

type Membership struct {
	OrganizationID string    `json:"organization_id"`
	AccountID      string    `json:"account_id"`
	Email          *string   `json:"email,omitempty"`
	Provider       string    `json:"provider,omitempty"`
	ProviderLogin  string    `json:"provider_login,omitempty"`
	Role           string    `json:"role"`
	CreatedAt      time.Time `json:"created_at"`
}

// OrganizationInvitation is the safe projection of a pending or historical
// organization invitation. The opaque token and its hash are deliberately not
// part of this DTO.
type OrganizationInvitation struct {
	ID                 string     `json:"id"`
	OrganizationID     string     `json:"organization_id"`
	Email              string     `json:"email"`
	Role               string     `json:"role"`
	InvitedByAccountID *string    `json:"invited_by_account_id,omitempty"`
	InvitedByEmail     *string    `json:"invited_by_email,omitempty"`
	Status             string     `json:"status"`
	ExpiresAt          time.Time  `json:"expires_at"`
	AcceptedAt         *time.Time `json:"accepted_at,omitempty"`
	RevokedAt          *time.Time `json:"revoked_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// OrganizationIncident is a durable operational record visible to every
// organization member. Updates contain the operator-authored timeline; no
// provider credentials or telemetry payloads are embedded here.
type OrganizationIncident struct {
	ID                 string                       `json:"id"`
	OrganizationID     string                       `json:"organization_id"`
	CreatedByAccountID *string                      `json:"created_by_account_id,omitempty"`
	CreatedByEmail     *string                      `json:"created_by_email,omitempty"`
	Title              string                       `json:"title"`
	Severity           string                       `json:"severity"`
	Status             string                       `json:"status"`
	Services           []string                     `json:"services"`
	StartedAt          time.Time                    `json:"started_at"`
	ResolvedAt         *time.Time                   `json:"resolved_at,omitempty"`
	Updates            []OrganizationIncidentUpdate `json:"updates"`
	CreatedAt          time.Time                    `json:"created_at"`
	UpdatedAt          time.Time                    `json:"updated_at"`
}

type OrganizationIncidentUpdate struct {
	ID              string    `json:"id"`
	IncidentID      string    `json:"incident_id"`
	AuthorAccountID *string   `json:"author_account_id,omitempty"`
	AuthorEmail     *string   `json:"author_email,omitempty"`
	Status          string    `json:"status"`
	Message         string    `json:"message"`
	CreatedAt       time.Time `json:"created_at"`
}

// HTTPTrace is the durable root-request index shown in the operator Console.
// Nested spans and attributes remain in the private OpenTelemetry backend;
// this projection contains only bounded tenant-safe request metadata.
type HTTPTrace struct {
	ID               string    `json:"id"`
	TraceID          string    `json:"trace_id"`
	SpanID           *string   `json:"span_id,omitempty"`
	OrganizationID   *string   `json:"organization_id,omitempty"`
	ProjectID        *string   `json:"project_id,omitempty"`
	OrganizationName string    `json:"organization_name,omitempty"`
	ProjectName      string    `json:"project_name,omitempty"`
	Service          string    `json:"service"`
	Method           string    `json:"method"`
	Route            string    `json:"route"`
	Status           int       `json:"status"`
	DurationMS       int64     `json:"duration_ms"`
	ResponseBytes    int64     `json:"response_bytes"`
	StartedAt        time.Time `json:"started_at"`
	FinishedAt       time.Time `json:"finished_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// AuditEvent is the durable, tenant-scoped activity record emitted by
// control-plane mutations. Actor and target IDs are nullable because account
// cleanup and system workers may leave an event without a live row.
type AuditEvent struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id"`
	ActorAccountID *string         `json:"actor_account_id,omitempty"`
	ActorEmail     *string         `json:"actor_email,omitempty"`
	Action         string          `json:"action"`
	TargetType     string          `json:"target_type"`
	TargetID       *string         `json:"target_id,omitempty"`
	Metadata       json.RawMessage `json:"metadata"`
	CreatedAt      time.Time       `json:"created_at"`
}

// CloudflareConnection is the private, instance-scoped provider identity used
// by the trusted worker. APIToken is populated only after decryption and must
// never be serialized or included in API-facing projections.
type CloudflareConnection struct {
	AccountID                   string     `json:"-"`
	ConsoleZoneID               string     `json:"-"`
	ConsoleHostname             string     `json:"-"`
	TunnelID                    string     `json:"-"`
	TunnelName                  string     `json:"-"`
	ConsoleRecordID             string     `json:"-"`
	APIToken                    string     `json:"-"`
	WorkloadZoneID              string     `json:"-"`
	WorkloadZoneName            string     `json:"-"`
	WildcardHostname            string     `json:"-"`
	WildcardRecordID            string     `json:"-"`
	Status                      string     `json:"-"`
	EdgeTLSStatus               string     `json:"-"`
	EdgeTLSError                string     `json:"-"`
	ConsoleOriginDesired        string     `json:"-"`
	ConsoleOriginObserved       string     `json:"-"`
	ConsoleOriginStatus         string     `json:"-"`
	ConsoleOriginLastError      string     `json:"-"`
	ConsoleOriginUpdatedAt      *time.Time `json:"-"`
	ConsolePublicVerifiedAt     *time.Time `json:"-"`
	ConsolePublicVerifiedOrigin string     `json:"-"`
	LastReconciledAt            *time.Time `json:"-"`
	LastError                   string     `json:"-"`
	ConfiguredAt                *time.Time `json:"-"`
	UpdatedAt                   time.Time  `json:"-"`
	WorkloadBaseDomain          *string    `json:"-"`
}

type CloudflareRetiringWildcardDNS struct {
	RecordID string
	ZoneID   string
	Hostname string
	Target   string
}

// CloudflareRoutingStatus is the safe admin response. It intentionally has
// no token, ciphertext, tunnel token, account secret, or raw provider body.
type CloudflareRoutingStatus struct {
	Configured                  bool       `json:"configured"`
	Status                      string     `json:"status"`
	ConsoleHostname             string     `json:"console_hostname,omitempty"`
	WorkloadHostname            *string    `json:"workload_hostname"`
	Zone                        string     `json:"zone,omitempty"`
	EdgeTLSStatus               string     `json:"edge_tls_status"`
	EdgeTLSError                string     `json:"edge_tls_error,omitempty"`
	ConsoleOriginDesired        string     `json:"console_origin_desired"`
	ConsoleOriginObserved       string     `json:"console_origin_observed"`
	ConsoleOriginStatus         string     `json:"console_origin_status"`
	ConsoleOriginLastError      string     `json:"console_origin_last_error,omitempty"`
	ConsoleOriginUpdatedAt      *time.Time `json:"console_origin_updated_at,omitempty"`
	ConsolePublicVerifiedAt     *time.Time `json:"console_public_verified_at,omitempty"`
	ConsolePublicVerifiedOrigin string     `json:"console_public_verified_origin,omitempty"`
	LastReconciledAt            *time.Time `json:"last_reconciled_at,omitempty"`
	LastError                   string     `json:"last_error,omitempty"`
}

type CloudflareRoutingUpdate struct {
	ExpectedWorkloadBaseDomain *string
	WorkloadZoneID             string
	WorkloadZoneName           string
	WildcardHostname           string
	WildcardRecordID           string
	EdgeTLSStatus              string
	EdgeTLSError               string
	ConsoleOriginDesired       string
	ConsoleOriginObserved      string
}

// AdminOperation is a safe, instance-wide projection of durable work already
// owned by the control plane. It deliberately joins existing deployment,
// execution, Agent, cleanup, and backup records instead of introducing a
// second operations ledger.
type AdminOperation struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	Name        string     `json:"name"`
	ProjectID   *string    `json:"project_id,omitempty"`
	ProjectName *string    `json:"project_name,omitempty"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	DurationMS  int64      `json:"duration_ms"`
	Error       *string    `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// AdminOperationSummary contains only counts derived from durable control
// plane state. A zero value means no work is currently represented, not a
// fabricated telemetry measurement.
type AdminOperationSummary struct {
	ActiveDeployments int64 `json:"active_deployments"`
	QueuedJobs        int64 `json:"queued_jobs"`
	RunningJobs       int64 `json:"running_jobs"`
	FailedJobs        int64 `json:"failed_jobs"`
}

// AdminMonitor is the safe instance-level projection of a persisted monitor.
// Secret headers, request bodies, heartbeat tokens, and provider credentials
// are held encrypted by the control plane and never appear in this DTO.
type AdminMonitor struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Kind               string         `json:"kind"`
	Target             string         `json:"target"`
	IntervalSeconds    int            `json:"interval_seconds"`
	TimeoutMS          int            `json:"timeout_ms"`
	Enabled            bool           `json:"enabled"`
	PublicConfig       map[string]any `json:"config"`
	Status             string         `json:"status"`
	LastCheckedAt      *time.Time     `json:"last_checked_at,omitempty"`
	LastSuccessAt      *time.Time     `json:"last_success_at,omitempty"`
	LastFailureAt      *time.Time     `json:"last_failure_at,omitempty"`
	LastLatencyMS      *int64         `json:"last_latency_ms,omitempty"`
	LastStatusCode     *int           `json:"last_status_code,omitempty"`
	LastError          *string        `json:"last_error,omitempty"`
	LastHeartbeatAt    *time.Time     `json:"last_heartbeat_at,omitempty"`
	NextCheckAt        time.Time      `json:"next_check_at"`
	SecretConfigured   bool           `json:"secret_configured"`
	CreatedByAccountID *string        `json:"created_by_account_id,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// AdminMonitorCheck is a bounded monitor execution record. Details contain
// only protocol-safe facts, never response bodies or credentials.
type AdminMonitorCheck struct {
	ID         string         `json:"id"`
	MonitorID  string         `json:"monitor_id"`
	CheckedAt  time.Time      `json:"checked_at"`
	Success    bool           `json:"success"`
	LatencyMS  int64          `json:"latency_ms"`
	StatusCode *int           `json:"status_code,omitempty"`
	Error      *string        `json:"error,omitempty"`
	Details    map[string]any `json:"details"`
}

// AdminAlertRule is the durable, owner-configured alert definition and its
// current state. Condition is constrained by the API and evaluated by the
// trusted worker; it is not an arbitrary SQL or expression payload.
type AdminAlertRule struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Kind               string         `json:"kind"`
	Condition          map[string]any `json:"condition"`
	Severity           string         `json:"severity"`
	ForSeconds         int            `json:"for_seconds"`
	Enabled            bool           `json:"enabled"`
	State              string         `json:"state"`
	PendingSince       *time.Time     `json:"pending_since,omitempty"`
	LastEvaluatedAt    *time.Time     `json:"last_evaluated_at,omitempty"`
	LastValue          *float64       `json:"last_value,omitempty"`
	LastError          *string        `json:"last_error,omitempty"`
	CreatedByAccountID *string        `json:"created_by_account_id,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type AdminAlertEvent struct {
	ID               string    `json:"id"`
	RuleID           string    `json:"rule_id"`
	RuleName         string    `json:"rule_name"`
	RuleKind         string    `json:"rule_kind"`
	Severity         string    `json:"severity"`
	State            string    `json:"state"`
	Value            *float64  `json:"value,omitempty"`
	Message          string    `json:"message"`
	OccurredAt       time.Time `json:"occurred_at"`
	SourceRuleExists bool      `json:"source_rule_exists"`
}

// AdminNotificationChannel never exposes its encrypted configuration. The
// worker decrypts it only while constructing a bounded delivery request.
type AdminNotificationChannel struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Kind               string     `json:"kind"`
	Enabled            bool       `json:"enabled"`
	SecretConfigured   bool       `json:"secret_configured"`
	LastDeliveryAt     *time.Time `json:"last_delivery_at,omitempty"`
	LastDeliveryStatus *string    `json:"last_delivery_status,omitempty"`
	LastError          *string    `json:"last_error,omitempty"`
	CreatedByAccountID *string    `json:"created_by_account_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type AdminNotificationDelivery struct {
	ID           string     `json:"id"`
	ChannelID    string     `json:"channel_id"`
	AlertEventID *string    `json:"alert_event_id,omitempty"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	AvailableAt  time.Time  `json:"available_at"`
	LastError    *string    `json:"last_error,omitempty"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

type AdminIncident struct {
	ID                 string               `json:"id"`
	Title              string               `json:"title"`
	Severity           string               `json:"severity"`
	Status             string               `json:"status"`
	Services           []string             `json:"services"`
	StartedAt          time.Time            `json:"started_at"`
	ResolvedAt         *time.Time           `json:"resolved_at,omitempty"`
	CreatedByAccountID *string              `json:"created_by_account_id,omitempty"`
	Events             []AdminIncidentEvent `json:"events"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
}

type AdminIncidentEvent struct {
	ID             string    `json:"id"`
	IncidentID     string    `json:"incident_id"`
	Kind           string    `json:"kind"`
	Message        string    `json:"message"`
	ActorAccountID *string   `json:"actor_account_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type AdminDashboard struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Description        string         `json:"description"`
	Definition         map[string]any `json:"definition"`
	CreatedByAccountID *string        `json:"created_by_account_id,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type AdminStatusPage struct {
	Name               string           `json:"name"`
	Description        string           `json:"description"`
	IsPublic           bool             `json:"is_public"`
	Components         []map[string]any `json:"components"`
	PublishedIncidents []string         `json:"published_incidents"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

// AdminPublicStatusPage is deliberately smaller than the owner configuration
// projection. It contains only components and incidents explicitly published
// by the owner; incident notes, audit data, and internal diagnostics stay
// private.
type AdminPublicStatusPage struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Components  []map[string]any      `json:"components"`
	Incidents   []AdminPublicIncident `json:"incidents"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

type AdminPublicIncident struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Severity   string     `json:"severity"`
	Status     string     `json:"status"`
	Services   []string   `json:"services"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// InstanceDomainSettings separates the Console hostname derived from
// PUBLIC_APP_URL from the optional workload-hosting suffix stored in
// PostgreSQL.
type InstanceDomainSettings struct {
	InstanceHostname   string  `json:"instance_hostname"`
	WorkloadBaseDomain *string `json:"workload_base_domain"`
}

type Project struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"created_at"`
}

// ProjectServiceLayout stores the durable editor position for one resource in
// a project's Services canvas. Resource IDs are validated against their
// project-owned table by the repository because the resource types are
// intentionally represented by one polymorphic projection table.
type ProjectServiceLayout struct {
	ProjectID    string    `json:"project_id"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	X            int       `json:"x"`
	Y            int       `json:"y"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Agent is the persisted configuration for a project coding agent. Provider
// credentials and execution output are deliberately not part of this
// projection; execution output belongs to AgentRun resources.
type Agent struct {
	ID                 string     `json:"id"`
	ProjectID          string     `json:"project_id"`
	ProjectName        string     `json:"project_name"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	Role               string     `json:"role"`
	Status             string     `json:"status"`
	Branch             string     `json:"branch"`
	Provider           string     `json:"provider"`
	Model              string     `json:"model"`
	CurrentTask        *string    `json:"current_task,omitempty"`
	LastActiveAt       *time.Time `json:"last_active_at,omitempty"`
	Tools              []string   `json:"tools"`
	Instructions       *string    `json:"instructions,omitempty"`
	CreatedByAccountID *string    `json:"created_by_account_id,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// AgentRun is a durable request to execute an Agent. A run may remain queued
// until an execution worker and provider connection are available; the API
// never turns an accepted request into a fabricated completed response.
type AgentRun struct {
	ID                 string           `json:"id"`
	AgentID            string           `json:"agent_id"`
	ProjectID          string           `json:"project_id"`
	Prompt             string           `json:"prompt"`
	Status             string           `json:"status"`
	OutputText         *string          `json:"output_text,omitempty"`
	ErrorMessage       *string          `json:"error_message,omitempty"`
	Steps              []AgentRunStep   `json:"steps"`
	Changes            []AgentRunChange `json:"changes"`
	CreatedByAccountID *string          `json:"created_by_account_id,omitempty"`
	QueuedAt           time.Time        `json:"queued_at"`
	StartedAt          *time.Time       `json:"started_at,omitempty"`
	FinishedAt         *time.Time       `json:"finished_at,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type AgentRunStep struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Label  string `json:"label"`
	Target string `json:"target"`
	Status string `json:"status"`
}

type AgentRunChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"`
}

type AgentRunLog struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	ProjectID string    `json:"project_id"`
	Sequence  int64     `json:"sequence"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// ProjectUsage is a point-in-time aggregate for the project Console. Values
// are derived from tenant-owned PostgreSQL rows; no request or billing data is
// guessed when the underlying subsystem has no durable counter yet.
type ProjectUsage struct {
	ProjectID                  string    `json:"project_id"`
	CapturedAt                 time.Time `json:"captured_at"`
	ApplicationUsers           int64     `json:"application_users"`
	DatabaseCount              int64     `json:"database_count"`
	DatabaseTableCount         int64     `json:"database_table_count"`
	DatabaseRowCount           int64     `json:"database_row_count"`
	StorageFileCount           int64     `json:"storage_file_count"`
	StorageBytes               int64     `json:"storage_bytes"`
	StorageQuotaBytes          int64     `json:"storage_quota_bytes"`
	FunctionCount              int64     `json:"function_count"`
	FunctionArtifactBytes      int64     `json:"function_artifact_bytes"`
	FunctionQuotaBytes         int64     `json:"function_quota_bytes"`
	SiteCount                  int64     `json:"site_count"`
	SiteArtifactBytes          int64     `json:"site_artifact_bytes"`
	SiteReservedBytes          int64     `json:"site_reserved_bytes"`
	SiteQuotaBytes             int64     `json:"site_quota_bytes"`
	RealtimeEventCount         int64     `json:"realtime_event_count"`
	WebhookDeliveryCount7      int64     `json:"webhook_delivery_count_7d"`
	APIRequestCount30D         int64     `json:"api_request_count_30d"`
	APIEgressBytes30D          int64     `json:"api_egress_bytes_30d"`
	FunctionInvocationCount30D int64     `json:"function_invocation_count_30d"`
	FunctionFailureCount30D    int64     `json:"function_failure_count_30d"`
	FunctionComputeMS30D       int64     `json:"function_compute_ms_30d"`
}

// ProjectUsageDay is one durable UTC calendar bucket. Missing days are not
// synthesized; callers can fill gaps when rendering charts without confusing
// unavailable data with a measured zero.
type ProjectUsageDay struct {
	Date                    string `json:"date"`
	APIRequestCount         int64  `json:"api_request_count"`
	APIEgressBytes          int64  `json:"api_egress_bytes"`
	FunctionInvocationCount int64  `json:"function_invocation_count"`
	FunctionFailureCount    int64  `json:"function_failure_count"`
	FunctionComputeMS       int64  `json:"function_compute_ms"`
}

type ProjectUsageMetering struct {
	ProjectID string            `json:"project_id"`
	From      string            `json:"from"`
	To        string            `json:"to"`
	Days      []ProjectUsageDay `json:"days"`
	Totals    ProjectUsageDay   `json:"totals"`
}

// ApplicationUser is a user belonging to a project application. It is
// intentionally separate from Account, which represents a Console operator.
// Password hashes are never part of this DTO.
type ApplicationUser struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	Email         string    `json:"email"`
	Name          *string   `json:"name"`
	Status        string    `json:"status"`
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ProjectAuthSettings struct {
	ProjectID           string    `json:"project_id"`
	RegistrationEnabled bool      `json:"registration_enabled"`
	CORSOrigins         []string  `json:"cors_origins"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// ProjectAPIKey is the safe management projection. Secret and hash material
// are deliberately absent; the full secret is returned only at creation.
type ProjectAPIKey struct {
	ID         string     `json:"id"`
	ProjectID  string     `json:"project_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ProjectAPIKeyAuth is internal request actor data. It is never serialized.
type ProjectAPIKeyAuth struct {
	ID         string
	ProjectID  string
	Scopes     []string
	LastUsedAt *time.Time
}

// Webhook contains delivery configuration without secret material. The
// plaintext signing secret is returned only by create/rotate responses.
type Webhook struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	Events         []string   `json:"events"`
	Enabled        bool       `json:"enabled"`
	FailureCount   int        `json:"failure_count"`
	LastDeliveryAt *time.Time `json:"last_delivery_at,omitempty"`
	LastFailureAt  *time.Time `json:"last_failure_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type WebhookDelivery struct {
	ID             string     `json:"id"`
	WebhookID      string     `json:"webhook_id"`
	EventID        string     `json:"event_id"`
	EventName      string     `json:"event_name"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	LastStatusCode *int       `json:"last_status_code,omitempty"`
	LastError      *string    `json:"last_error,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// MessagingProvider is a safe project provider projection. Credentials are
// encrypted at rest and represented only by the configured flag; plaintext
// values and ciphertext are never serialized.
type MessagingProvider struct {
	ID                 string    `json:"id"`
	ProjectID          string    `json:"project_id"`
	Name               string    `json:"name"`
	Channel            string    `json:"channel"`
	Provider           string    `json:"provider"`
	CredentialsPresent bool      `json:"credentials_present"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// MessagingTopic is a project-scoped fan-out target. SubscriberCount is
// computed from durable subscriber rows and is safe to show in the Console.
type MessagingTopic struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Enabled         bool      `json:"enabled"`
	SubscriberCount int64     `json:"subscriber_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// MessagingSubscriber keeps the recipient secret while returning a bounded
// masked preview for operators. The full address is never returned by the API.
type MessagingSubscriber struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	TopicID        string    `json:"topic_id"`
	Channel        string    `json:"channel"`
	AddressPreview string    `json:"address_preview"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// MessagingMessage is a safe delivery projection. The message body, subject,
// and optional data are encrypted at rest and are intentionally omitted from
// API responses.
type MessagingMessage struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	TopicID        *string    `json:"topic_id"`
	Channel        string     `json:"channel"`
	Status         string     `json:"status"`
	RecipientCount int64      `json:"recipient_count"`
	SucceededCount int64      `json:"succeeded_count"`
	FailedCount    int64      `json:"failed_count"`
	CancelledAt    *time.Time `json:"cancelled_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// MessagingDelivery is a safe per-recipient status projection. The recipient
// address is represented only by the masked preview captured at enqueue time.
type MessagingDelivery struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	MessageID      string     `json:"message_id"`
	SubscriberID   *string    `json:"subscriber_id,omitempty"`
	ProviderID     *string    `json:"provider_id,omitempty"`
	Channel        string     `json:"channel"`
	AddressPreview string     `json:"address_preview"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	LastStatusCode *int       `json:"last_status_code,omitempty"`
	LastError      *string    `json:"last_error,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// RealtimeEvent is the short-lived project event envelope consumed by the
// Realtime SSE transport. Payload contains the exact JSON envelope persisted
// in the transactional outbox and is kept out of ordinary JSON projections so
// handlers can stream it without re-encoding or changing signatures.
type RealtimeEvent struct {
	ID             string
	OrganizationID string
	ProjectID      string
	EventName      string
	Version        int
	TargetType     string
	TargetID       *string
	ResourceID     *string
	CorrelationID  string
	Data           map[string]any
	OccurredAt     time.Time
	CreatedAt      time.Time
	Payload        json.RawMessage
}

type ProjectDatabase struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DatabaseBackup describes an immutable logical snapshot. The storage path is
// intentionally omitted; callers receive a download endpoint instead of an
// internal filesystem/S3 key.
type DatabaseBackup struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	DatabaseID     string    `json:"database_id"`
	SizeBytes      int64     `json:"size_bytes"`
	ChecksumSHA256 string    `json:"checksum_sha256"`
	CreatedAt      time.Time `json:"created_at"`
}

type DatabaseTable struct {
	ID                string    `json:"id"`
	DatabaseID        string    `json:"database_id"`
	ProjectID         string    `json:"project_id"`
	Name              string    `json:"name"`
	RowSecurity       bool      `json:"row_security"`
	CreatePermissions []string  `json:"create_permissions"`
	ReadPermissions   []string  `json:"read_permissions"`
	UpdatePermissions []string  `json:"update_permissions"`
	DeletePermissions []string  `json:"delete_permissions"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type DatabaseColumn struct {
	ID          string          `json:"id"`
	TableID     string          `json:"table_id"`
	Key         string          `json:"key"`
	Type        string          `json:"type"`
	Required    bool            `json:"required"`
	VarcharSize *int            `json:"varchar_size,omitempty"`
	Default     json.RawMessage `json:"default,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type DatabaseIndex struct {
	ID         string    `json:"id"`
	TableID    string    `json:"table_id"`
	Name       string    `json:"name"`
	Type       string    `json:"type"`
	ColumnKeys []string  `json:"column_keys"`
	Directions []string  `json:"directions"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DatabaseRelationship is a tenant-scoped many-to-one reference. The source
// value is the UUID of a row in TargetTableID; the repository validates that
// target on every source-row write and protects it from deletion while used.
type DatabaseRelationship struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	DatabaseID       string    `json:"database_id"`
	SourceTableID    string    `json:"source_table_id"`
	SourceColumnKey  string    `json:"source_column_key"`
	TargetTableID    string    `json:"target_table_id"`
	RelationshipType string    `json:"type"`
	OnDelete         string    `json:"on_delete"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type DatabaseRow struct {
	ID                   string         `json:"id"`
	TableID              string         `json:"table_id"`
	ProjectID            string         `json:"project_id"`
	Data                 map[string]any `json:"data"`
	ReadPermissions      []string       `json:"read_permissions"`
	UpdatePermissions    []string       `json:"update_permissions"`
	DeletePermissions    []string       `json:"delete_permissions"`
	CreatorProjectUserID *string        `json:"creator_project_user_id,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

// StorageBucket is safe to return to Console and project data callers. The
// filesystem path is intentionally not part of this DTO.
type StorageBucket struct {
	ID                string    `json:"id"`
	ProjectID         string    `json:"project_id"`
	Name              string    `json:"name"`
	FileSecurity      bool      `json:"file_security"`
	CreatePermissions []string  `json:"create_permissions"`
	ReadPermissions   []string  `json:"read_permissions"`
	UpdatePermissions []string  `json:"update_permissions"`
	DeletePermissions []string  `json:"delete_permissions"`
	MaxFileSizeBytes  int64     `json:"max_file_size_bytes"`
	QuotaBytes        int64     `json:"quota_bytes"`
	UsedBytes         int64     `json:"used_bytes"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// StorageFile contains metadata only. Blob bytes are always streamed from the
// local store and are never serialized into PostgreSQL or this response.
type StorageFile struct {
	ID                   string    `json:"id"`
	BucketID             string    `json:"bucket_id"`
	ProjectID            string    `json:"project_id"`
	Name                 string    `json:"name"`
	MimeType             string    `json:"mime_type"`
	SizeBytes            int64     `json:"size_bytes"`
	ChecksumSHA256       string    `json:"checksum_sha256"`
	ReadPermissions      []string  `json:"read_permissions"`
	UpdatePermissions    []string  `json:"update_permissions"`
	DeletePermissions    []string  `json:"delete_permissions"`
	CreatorProjectUserID *string   `json:"creator_project_user_id,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Function is the tenant-scoped metadata for a deployable function. Source
// bytes and variable values are intentionally absent from this DTO.
type Function struct {
	ID                    string    `json:"id"`
	ProjectID             string    `json:"project_id"`
	Name                  string    `json:"name"`
	Runtime               string    `json:"runtime"`
	Entrypoint            string    `json:"entrypoint"`
	Commands              string    `json:"commands"`
	TimeoutSeconds        int       `json:"timeout_seconds"`
	Enabled               bool      `json:"enabled"`
	Logging               bool      `json:"logging"`
	ExecutePermissions    []string  `json:"execute_permissions"`
	Description           *string   `json:"description,omitempty"`
	Status                string    `json:"status"`
	ArtifactQuotaBytes    int64     `json:"artifact_quota_bytes"`
	ArtifactUsedBytes     int64     `json:"artifact_used_bytes"`
	ArtifactReservedBytes int64     `json:"artifact_reserved_bytes"`
	ActiveDeploymentID    *string   `json:"active_deployment_id,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// FunctionVariable is metadata only. HasValue lets a caller distinguish an
// unset variable from an explicitly configured one without exposing either a
// plaintext value or encrypted/hash material.
type FunctionVariable struct {
	ID          string    `json:"id"`
	FunctionID  string    `json:"function_id"`
	ProjectID   string    `json:"project_id"`
	Key         string    `json:"key"`
	Kind        string    `json:"kind"`
	IsSecret    bool      `json:"is_secret"`
	HasValue    bool      `json:"has_value"`
	Description *string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// FunctionDeployment exposes build/activation metadata while keeping the
// UUID-derived source path private.
type FunctionDeployment struct {
	ID                 string     `json:"id"`
	FunctionID         string     `json:"function_id"`
	ProjectID          string     `json:"project_id"`
	Version            int64      `json:"version"`
	Source             string     `json:"source"`
	SourceName         *string    `json:"source_name,omitempty"`
	SizeBytes          int64      `json:"size_bytes"`
	ChecksumSHA256     string     `json:"checksum_sha256"`
	Status             string     `json:"status"`
	BuildStatus        string     `json:"build_status"`
	ErrorMessage       *string    `json:"error_message,omitempty"`
	CreatedByAccountID *string    `json:"created_by_account_id,omitempty"`
	QueuedAt           time.Time  `json:"queued_at"`
	BuildStartedAt     *time.Time `json:"build_started_at,omitempty"`
	BuiltAt            *time.Time `json:"built_at,omitempty"`
	ActivatedAt        *time.Time `json:"activated_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type FunctionBuildLog struct {
	ID           string    `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	FunctionID   string    `json:"function_id"`
	ProjectID    string    `json:"project_id"`
	Sequence     int64     `json:"sequence"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"created_at"`
}

type FunctionExecution struct {
	ID                string          `json:"id"`
	DeploymentID      string          `json:"deployment_id"`
	FunctionID        string          `json:"function_id"`
	ProjectID         string          `json:"project_id"`
	Status            string          `json:"status"`
	Trigger           string          `json:"trigger"`
	InputJSON         json.RawMessage `json:"input_json,omitempty"`
	ResponseStatus    *int            `json:"response_status,omitempty"`
	OutputJSON        json.RawMessage `json:"output_json,omitempty"`
	OutputContentType *string         `json:"output_content_type,omitempty"`
	ErrorMessage      *string         `json:"error_message,omitempty"`
	StartedAt         *time.Time      `json:"started_at,omitempty"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type FunctionExecutionLog struct {
	ID          string    `json:"id"`
	ExecutionID string    `json:"execution_id"`
	FunctionID  string    `json:"function_id"`
	ProjectID   string    `json:"project_id"`
	Sequence    int64     `json:"sequence"`
	Level       string    `json:"level"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}

// Site is project-scoped static hosting metadata. Files are kept in a
// private immutable directory and are intentionally absent from this DTO.
type Site struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
	// PlatformHostname is derived from the stable database platform label and
	// the current optional instance workload domain. It is null until an
	// operator configures that domain; the label itself is intentionally not a
	// public API field.
	PlatformHostname      *string   `json:"platform_hostname"`
	Framework             string    `json:"framework"`
	Enabled               bool      `json:"enabled"`
	Status                string    `json:"status"`
	ArtifactQuotaBytes    int64     `json:"artifact_quota_bytes"`
	ArtifactUsedBytes     int64     `json:"artifact_used_bytes"`
	ArtifactReservedBytes int64     `json:"artifact_reserved_bytes"`
	ActiveDeploymentID    *string   `json:"active_deployment_id,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// App is durable desired configuration for a persistent project workload.
// Runtime status and observed generation remain owned by a trusted runtime
// reconciler; creating or updating metadata never claims that work ran.
type App struct {
	ID                 string            `json:"id"`
	ProjectID          string            `json:"project_id"`
	Name               string            `json:"name"`
	Enabled            bool              `json:"enabled"`
	PlatformHostname   *string           `json:"platform_hostname"`
	Workload           workloadspec.Spec `json:"workload"`
	WorkloadSpecSHA256 string            `json:"workload_spec_sha256"`
	DesiredGeneration  int64             `json:"desired_generation"`
	ObservedGeneration int64             `json:"observed_generation"`
	RuntimeStatus      string            `json:"runtime_status"`
	RuntimeError       *string           `json:"runtime_error"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

// PlatformRoute is the PostgreSQL-derived desired state consumed by the
// Traefik file-provider reconciler. It contains no filesystem or router
// implementation details.
type PlatformRoute struct {
	SiteID   string
	Hostname string
}

// SiteDomain binds a verified DNS hostname to a Site. The verification token
// is a public challenge value, not an API credential; it is returned so an
// operator can publish the required TXT record.
type SiteDomain struct {
	ID                      string     `json:"id"`
	ProjectID               string     `json:"project_id"`
	SiteID                  string     `json:"site_id"`
	Hostname                string     `json:"hostname"`
	Status                  string     `json:"status"`
	VerificationToken       string     `json:"verification_token"`
	VerificationRecordName  string     `json:"verification_record_name"`
	VerificationRecordType  string     `json:"verification_record_type"`
	VerificationRecordValue string     `json:"verification_record_value"`
	VerifiedAt              *time.Time `json:"verified_at,omitempty"`
	TLSStatus               string     `json:"tls_status"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// SiteDeployment exposes publication metadata while keeping the internal
// UUID-derived artifact path private.
type SiteDeployment struct {
	ID                 string     `json:"id"`
	SiteID             string     `json:"site_id"`
	ProjectID          string     `json:"project_id"`
	Version            int64      `json:"version"`
	Source             string     `json:"source"`
	SourceName         *string    `json:"source_name,omitempty"`
	GitRepository      *string    `json:"git_repository,omitempty"`
	GitRef             *string    `json:"git_ref,omitempty"`
	SizeBytes          int64      `json:"size_bytes"`
	ArchiveSizeBytes   int64      `json:"archive_size_bytes"`
	ChecksumSHA256     string     `json:"checksum_sha256"`
	Status             string     `json:"status"`
	BuildRuntime       string     `json:"build_runtime,omitempty"`
	BuildCommand       string     `json:"build_command,omitempty"`
	OutputDirectory    string     `json:"output_directory,omitempty"`
	BuildStatus        string     `json:"build_status"`
	ActivateRequested  bool       `json:"activate_requested,omitempty"`
	ReservedBytes      int64      `json:"reserved_bytes,omitempty"`
	ErrorMessage       *string    `json:"error_message,omitempty"`
	CreatedByAccountID *string    `json:"created_by_account_id,omitempty"`
	QueuedAt           time.Time  `json:"queued_at"`
	BuildStartedAt     *time.Time `json:"build_started_at,omitempty"`
	BuiltAt            *time.Time `json:"built_at,omitempty"`
	ActivatedAt        *time.Time `json:"activated_at,omitempty"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// SiteBuildLog is a bounded, secret-safe lifecycle message emitted by the
// trusted Site build worker. Deployment and tenant identifiers are included
// so callers can safely render logs without joining private storage paths.
type SiteBuildLog struct {
	ID           string    `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	SiteID       string    `json:"site_id"`
	ProjectID    string    `json:"project_id"`
	Sequence     int64     `json:"sequence"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	CreatedAt    time.Time `json:"created_at"`
}
