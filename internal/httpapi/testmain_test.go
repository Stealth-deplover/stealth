package httpapi_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/auth"
	"github.com/Stealth-deplover/stealth/internal/bootstrap"
	"github.com/Stealth-deplover/stealth/internal/migrate"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMain(main *testing.M) {
	if databaseURL := os.Getenv("TEST_DATABASE_URL"); databaseURL != "" {
		if err := prepareExistingHTTPIntegrationDatabase(databaseURL); err != nil {
			fmt.Fprintf(os.Stderr, "prepare HTTP integration database: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(main.Run())
}

func prepareExistingHTTPIntegrationDatabase(databaseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate.Apply(ctx, pool); err != nil {
		return err
	}
	var ownerCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM instance_roles WHERE role='instance_owner'`).Scan(&ownerCount); err != nil {
		return err
	}
	if ownerCount > 0 {
		return nil
	}
	code, err := bootstrap.GenerateCode()
	if err != nil {
		return err
	}
	repo := repository.New(pool)
	sessionID := uuid.Must(uuid.NewV7())
	if err := repo.CreateBootstrapSession(ctx, repository.BootstrapSessionInput{
		ID:        sessionID,
		CodeHash:  bootstrap.HashCode(code),
		ExpiresAt: time.Now().UTC().Add(bootstrap.CodeLifetime),
	}); err != nil {
		return err
	}
	_, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		return err
	}
	_, err = repo.CreateGitHubInstanceOwner(ctx, repository.GitHubOwnerInput{
		BootstrapSessionID: sessionID,
		BootstrapCodeHash:  bootstrap.HashCode(code),
		AccountID:          uuid.Must(uuid.NewV7()),
		SessionID:          uuid.Must(uuid.NewV7()),
		TokenHash:          tokenHash,
		SessionExpiresAt:   time.Now().UTC().Add(time.Hour),
		ProviderUserID:     "integration-owner",
		ProviderLogin:      "integration-owner",
		ProviderEmail:      "integration-owner@example.test",
		DisplayName:        "Integration Owner",
	})
	return err
}
