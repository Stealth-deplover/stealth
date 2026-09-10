package repository

import (
	"context"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool          *pgxpool.Pool
	txtResolver   SiteTXTResolver
	webhookCipher *functionsecret.Cipher
	// Messaging provider credentials and subscriber addresses use the same
	// process-held AES-GCM key as webhook secrets. Keeping the cipher on the
	// repository ensures reads can expose only safe metadata while trusted
	// workers can later decrypt values without changing the API contract.
	messagingCipher *functionsecret.Cipher
}

// Dependencies carries the optional repository collaborators. Zero values
// build a repository without Site TXT resolution or secret decryption.
type Dependencies struct {
	TXTResolver   SiteTXTResolver
	WebhookCipher *functionsecret.Cipher
}

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// NewWithDependencies builds a repository with injectable collaborators.
// The webhook and messaging ciphers intentionally share one key so both
// domains rotate together without changing the API contract.
func NewWithDependencies(pool *pgxpool.Pool, deps Dependencies) *Repository {
	return &Repository{pool: pool, txtResolver: deps.TXTResolver, webhookCipher: deps.WebhookCipher, messagingCipher: deps.WebhookCipher}
}

func (r *Repository) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }
