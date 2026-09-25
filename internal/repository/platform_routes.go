package repository

import (
	"context"
	"errors"
	"sync"

	"github.com/Stealth-deplover/stealth/internal/domain"
	"github.com/Stealth-deplover/stealth/internal/domainname"
	"github.com/Stealth-deplover/stealth/internal/platformhostname"
	"github.com/jackc/pgx/v5"
)

// This lock is session-scoped and is held from the desired-state snapshot
// through publication by the worker. It is intentionally distinct from the
// migration and realtime locks.
const platformRouteReconcileLockID int64 = 8_105_202_602

// TryPlatformRouteReconcileLock gives one worker a distributed single-writer
// lease. The returned release function must be called after the generated
// snapshot has been published (or after a failed attempt).
func (r *Repository) TryPlatformRouteReconcileLock(ctx context.Context) (func() error, bool, error) {
	if r == nil || r.pool == nil {
		return nil, false, ErrNotFound
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, platformRouteReconcileLockID).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}

	var releaseOnce sync.Once
	var releaseErr error
	return func() error {
		releaseOnce.Do(func() {
			_, releaseErr = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, platformRouteReconcileLockID)
			conn.Release()
		})
		return releaseErr
	}, true, nil
}

// ListPlatformRoutes returns the complete desired platform-hostname snapshot.
// PostgreSQL remains authoritative; generated YAML is never read here.
func (r *Repository) ListPlatformRoutes(ctx context.Context) ([]domain.PlatformRoute, error) {
	if r == nil || r.pool == nil {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `
		SELECT s.id::text,s.platform_label,d.workload_base_domain
		FROM project_sites s
		JOIN instance_domain_settings d ON d.id=TRUE
		WHERE d.workload_base_domain IS NOT NULL
		  AND s.platform_label IS NOT NULL
		  AND s.enabled=TRUE
		  AND s.status='active'
		ORDER BY s.platform_label,s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := make([]domain.PlatformRoute, 0)
	for rows.Next() {
		var siteID, label, baseDomain string
		if err := rows.Scan(&siteID, &label, &baseDomain); err != nil {
			return nil, err
		}
		hostname, err := platformhostname.Hostname(label, baseDomain)
		if err != nil {
			return nil, err
		}
		routes = append(routes, domain.PlatformRoute{SiteID: siteID, Hostname: hostname})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return routes, nil
}

// ListAppPlatformRoutes returns only Apps whose current observed runtime and
// fenced application health state permit public routing. The route identity
// is private worker state used to derive the Docker DNS target.
func (r *Repository) ListAppPlatformRoutes(ctx context.Context) ([]domain.AppPlatformRoute, error) {
	if r == nil || r.pool == nil {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `
		SELECT app.id::text,app.platform_label,settings.workload_base_domain,
		       runtime.route_identity::text,runtime.health_route_identity::text,runtime.container_name,
		       CASE WHEN app.workload_spec->>'port' ~ '^[0-9]{1,5}$'
		            THEN (app.workload_spec->>'port')::integer ELSE NULL END
		FROM project_apps app
		JOIN app_runtime_state runtime ON runtime.app_id=app.id AND runtime.project_id=app.project_id
		JOIN app_deployments deployment ON deployment.id=app.desired_deployment_id
		  AND deployment.app_id=app.id AND deployment.project_id=app.project_id
		  AND deployment.status='ready' AND deployment.build_status='succeeded'
		JOIN instance_domain_settings settings ON settings.id=TRUE
		WHERE app.enabled=TRUE
		  AND app.desired_deployment_id IS NOT NULL
		  AND app.desired_generation=app.observed_generation
		  AND app.runtime_status='running'
		  AND settings.workload_base_domain IS NOT NULL
		  AND app.platform_label IS NOT NULL
		  AND runtime.applied_generation=app.desired_generation
		  AND runtime.applied_deployment_id=app.desired_deployment_id
		  AND runtime.applied_workload_spec_sha256=app.workload_spec_sha256
		  AND runtime.container_id IS NOT NULL
		  AND runtime.container_address IS NOT NULL
		  AND runtime.health_status='healthy'
		  AND runtime.health_generation=app.desired_generation
		  AND runtime.health_deployment_id=app.desired_deployment_id
		  AND runtime.health_container_id=runtime.container_id
		  AND runtime.health_route_identity=runtime.route_identity
		  AND runtime.container_name='st-'||replace(app.id::text,'-','')||'-'||substring(replace(runtime.route_identity::text,'-','') FROM 1 FOR 24)
		ORDER BY app.platform_label,app.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	routes := make([]domain.AppPlatformRoute, 0)
	for rows.Next() {
		var appID, label, baseDomain string
		var routeIdentity, healthRouteIdentity, containerName *string
		var port *int32
		if err := rows.Scan(&appID, &label, &baseDomain, &routeIdentity, &healthRouteIdentity, &containerName, &port); err != nil {
			return nil, err
		}
		parsedID, idErr := ParseUUID(appID)
		hostname, hostErr := platformhostname.Hostname(label, baseDomain)
		validIdentity := idErr == nil && appRuntimeRouteIdentityMatches(parsedID, routeIdentity, healthRouteIdentity, containerName)
		if idErr != nil || hostErr != nil || !validIdentity || routeIdentity == nil || port == nil || *port < 1 || *port > 65535 {
			// Fail closed for this App only. A bad App record must not prevent
			// Site snapshots or other valid App routes from converging.
			continue
		}
		routes = append(routes, domain.AppPlatformRoute{AppID: parsedID.String(), RouteIdentity: *routeIdentity, Hostname: hostname, Port: int(*port)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return routes, nil
}

// GetActiveSiteArtifactByPlatformHostname resolves the current request Host
// against PostgreSQL state before opening an artifact. A stale Traefik router
// therefore cannot keep deleted or disabled Sites public.
func (r *Repository) GetActiveSiteArtifactByPlatformHostname(ctx context.Context, hostname string) (SitePublicArtifact, error) {
	if r == nil || r.pool == nil {
		return SitePublicArtifact{}, ErrNotFound
	}
	if hostname == "" {
		return SitePublicArtifact{}, ErrNotFound
	}
	normalizedHostname, err := domainname.NormalizeHostname(hostname)
	if err != nil {
		return SitePublicArtifact{}, ErrNotFound
	}
	var siteID, platformLabel, workloadBaseDomain string
	if err := r.pool.QueryRow(ctx, `
		SELECT s.id::text,s.platform_label,d.workload_base_domain
		FROM project_sites s
		JOIN instance_domain_settings d ON d.id=TRUE
		WHERE d.workload_base_domain IS NOT NULL
		  AND s.platform_label IS NOT NULL
		  AND s.enabled=TRUE
		  AND s.status='active'
		  AND s.platform_label || '.' || d.workload_base_domain = $1`, normalizedHostname).Scan(&siteID, &platformLabel, &workloadBaseDomain); errors.Is(err, pgx.ErrNoRows) {
		return SitePublicArtifact{}, ErrNotFound
	} else if err != nil {
		return SitePublicArtifact{}, err
	}
	derivedHostname, err := platformhostname.Hostname(platformLabel, workloadBaseDomain)
	if err != nil || derivedHostname != normalizedHostname {
		return SitePublicArtifact{}, ErrNotFound
	}
	parsedID, err := ParseUUID(siteID)
	if err != nil {
		return SitePublicArtifact{}, ErrNotFound
	}
	artifact, err := r.GetActiveSiteArtifact(ctx, parsedID)
	if err != nil {
		return SitePublicArtifact{}, err
	}
	// The domain setting can change between the route lookup and the artifact
	// lookup. Re-check the hostname derived from the latter current snapshot so
	// an old platform hostname does not remain usable across that boundary.
	if artifact.Site.PlatformHostname == nil || *artifact.Site.PlatformHostname != normalizedHostname {
		return SitePublicArtifact{}, ErrNotFound
	}
	return artifact, nil
}
