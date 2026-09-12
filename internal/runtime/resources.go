// Package runtime owns shared process resource composition.
//
// API, worker, and migration entry points use the same database pool policy
// and migration lifecycle through this module while retaining their own
// process-specific registration and execution behavior.
package runtime

import (
	"context"
	"fmt"

	"github.com/Stealth-deplover/stealth/internal/config"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type OpenOptions struct {
	WithRedis       bool
	ApplyMigrations bool
}

type Resources struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
}

func Open(ctx context.Context, cfg config.Config, options OpenOptions) (*Resources, error) {
	resources := &Resources{}
	if options.WithRedis {
		redisOptions, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return nil, fmt.Errorf("parse Redis configuration: %w", err)
		}
		resources.Redis = redis.NewClient(redisOptions)
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		resources.Close()
		return nil, fmt.Errorf("parse database configuration: %w", err)
	}
	poolConfig.MaxConns = cfg.DatabaseMaxConns
	poolConfig.MinConns = cfg.DatabaseMinConns
	poolConfig.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.DatabaseMaxConnIdleTime
	resources.Pool, err = pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		resources.Close()
		return nil, fmt.Errorf("open database connection: %w", err)
	}

	if options.ApplyMigrations {
		if err := migrate.Apply(ctx, resources.Pool); err != nil {
			resources.Close()
			return nil, fmt.Errorf("apply database migrations: %w", err)
		}
	}
	return resources, nil
}

func (r *Resources) Close() {
	if r == nil {
		return
	}
	if r.Redis != nil {
		_ = r.Redis.Close()
	}
	if r.Pool != nil {
		r.Pool.Close()
	}
}
