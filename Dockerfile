FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown
ENV BUILD_LDFLAGS="-s -w -X github.com/Stealth-deplover/stealth/internal/buildinfo.Version=${VERSION} -X github.com/Stealth-deplover/stealth/internal/buildinfo.Commit=${COMMIT_SHA} -X github.com/Stealth-deplover/stealth/internal/buildinfo.BuildTime=${BUILD_TIME}"
RUN CGO_ENABLED=0 go build -trimpath -ldflags="${BUILD_LDFLAGS}" -o /out/stealth-api ./cmd/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags="${BUILD_LDFLAGS}" -o /out/stealth-worker ./cmd/worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="${BUILD_LDFLAGS}" -o /out/stealth-migrate ./cmd/migrate

FROM alpine:3.24 AS runtime-base
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_TIME=unknown
RUN apk add --no-cache ca-certificates wget && addgroup -S stealth && adduser -S -G stealth stealth
RUN mkdir -p /var/lib/stealth/storage /var/lib/stealth/runner-staging && chown -R stealth:stealth /var/lib/stealth
VOLUME ["/var/lib/stealth/storage", "/var/lib/stealth/runner-staging"]
WORKDIR /app
LABEL org.opencontainers.image.title="Stealth" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT_SHA" \
      org.opencontainers.image.created="$BUILD_TIME" \
      org.opencontainers.image.source="https://github.com/Stealth-deplover/stealth"

FROM runtime-base AS api
COPY --from=build /out/stealth-api /usr/local/bin/stealth-api
USER stealth
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/stealth-api"]

FROM runtime-base AS worker
RUN apk add --no-cache docker-cli
COPY --from=build /out/stealth-worker /usr/local/bin/stealth-worker
USER stealth
EXPOSE 9091
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD wget -qO- http://127.0.0.1:9091/healthz >/dev/null || exit 1
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/stealth-worker"]

FROM runtime-base AS migrate
COPY --from=build /out/stealth-migrate /usr/local/bin/stealth-migrate
USER stealth
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/stealth-migrate"]
