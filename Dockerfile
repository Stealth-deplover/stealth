FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/stealth-api ./cmd/api
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/stealth-worker ./cmd/worker

FROM alpine:3.22 AS runtime-base
RUN addgroup -S stealth && adduser -S -G stealth stealth
RUN mkdir -p /var/lib/stealth/storage /var/lib/stealth/runner-staging && chown -R stealth:stealth /var/lib/stealth
VOLUME ["/var/lib/stealth/storage", "/var/lib/stealth/runner-staging"]
WORKDIR /app
COPY --from=build /out/stealth-api /usr/local/bin/stealth-api
COPY --from=build /out/stealth-worker /usr/local/bin/stealth-worker

FROM runtime-base AS api
USER stealth
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/stealth-api"]

FROM runtime-base AS worker
RUN apk add --no-cache docker-cli
USER stealth
EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/stealth-worker"]
