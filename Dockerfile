# syntax=docker/dockerfile:1

# Builds both binaries (cmd/api, the HTTP server; cmd/viatuta, the operator
# CLI for ingest/scoring) plus goose, so migrations can be applied by the
# same image without a separate migration tool baked into the base OS.
FROM golang:1.25-bookworm AS builder
WORKDIR /src

# Every dependency here (pgx, paulmach/osm) is pure Go — confirmed by a
# CGO_ENABLED=0 build succeeding for both binaries — so the runtime image
# can be a static, libc-free base.
ENV GOTOOLCHAIN=auto
ENV CGO_ENABLED=0

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api
RUN go build -trimpath -ldflags="-s -w" -o /out/viatuta ./cmd/viatuta
RUN go install github.com/pressly/goose/v3/cmd/goose@latest \
    && cp "$(go env GOPATH)/bin/goose" /out/goose

FROM alpine:3.20 AS runtime
RUN apk add --no-cache ca-certificates \
    && addgroup -S viatuta && adduser -S viatuta -G viatuta

WORKDIR /app
COPY --from=builder /out/api /out/viatuta /out/goose ./
COPY db/migrations ./db/migrations
COPY docker-entrypoint.sh ./
RUN chmod +x /app/docker-entrypoint.sh /app/api /app/viatuta /app/goose

USER viatuta
EXPOSE 8080

# The entrypoint applies pending migrations, then execs whatever CMD (or an
# overriding `docker compose run` command) was given — the API server by
# default, or the operator CLI for one-off ingest/scoring jobs.
ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/api"]
