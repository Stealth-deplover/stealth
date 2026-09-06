# Stealth API

Backend Go untuk **Stealth** — developer cloud control plane. Repo ini hanya
berisi API dan worker; frontend (TanStack Start console) hidup di repo
terpisah.

- **Go 1.26** dengan Chi router, pgx/v5, sqlc, dan Redis
- PostgreSQL sebagai datastore (migrasi embedded, transactional)
- Sesi opaque berbasis cookie HttpOnly (Argon2id untuk password)
- Worker terpisah untuk Functions build/exec, Sites build, webhook, dan
  messaging (lease + retry, artifact di storage lokal/S3)
- `/metrics` Prometheus, tracing OpenTelemetry opsional

## Layout

```
cmd/api        entrypoint API (graceful shutdown)
cmd/worker     entrypoint worker (build, execution, delivery)
internal/
  httpapi      route registration, handler, middleware (Chi)
  repository   akses data per domain (pgx + sqlc)
  config       load env, validasi, default
  functionrunner / agentrunner / messagingrunner / webhookrunner
               worker pipelines
  functionstore / sitestore / storage   artifact & blob stores
  auth, apikey, mailer, ratelimit, observability, tlsmanager, ...
openapi/openapi.yaml   kontrak REST (OpenAPI 3.1)
```

## Menjalankan

```bash
cp .env.example .env   # atau set minimal: DATABASE_URL, REDIS_URL, FUNCTIONS_SECRET_KEY
go run ./cmd/api       # API di :8080 (migrasi otomatis diterapkan)
go run ./cmd/worker    # worker
```

Variabel environment utama didefinisikan dan divalidasi di
`internal/config/config.go` (lihat `Load()`): `DATABASE_URL`, `REDIS_URL`,
`HTTP_ADDR`, `FUNCTIONS_SECRET_KEY`, `STORAGE_*`, `SMTP_*`, `AUTH_*`,
`CONSOLE_CORS_ORIGINS`, dst.

### Docker

```bash
docker build --target api -t stealth-api .     # binary API
docker build --target worker -t stealth-worker .  # binary worker
```

## Testing

```bash
go vet ./...
go test ./...            # unit; integration otomatis skip tanpa TEST_DATABASE_URL
TEST_DATABASE_URL=postgres://stealth:postgres@127.0.0.1:5432/stealth?sslmode=disable \
TEST_REDIS_URL=redis://127.0.0.1:6379/0 \
  go test ./internal/httpapi -run Integration -count=1
go test -race ./...      # race check penuh
```

## Kontrak API

`openapi/openapi.yaml` mendeskripsikan seluruh endpoint `/v1`. Perubahan
endpoint harus tetap kompatibel dengan file tersebut.
