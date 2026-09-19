# Every recipe runs inside containers; Docker and just are the only local
# requirements. `.env` is loaded automatically for compose substitutions.

set shell := ["sh", "-c"]
set dotenv-load := true

compose := "docker compose -f compose.yaml -f compose.dev.yaml"
prod := "docker compose -f compose.yaml"
image := "ghcr.io/anesthetised/couchcast"

[private]
default:
    @just --list

# --- stack -----------------------------------------------------------------

[group('stack')]
up:
    {{compose}} up -d postgres minio minio-init

[group('stack')]
down:
    {{compose}} down

[group('stack')]
logs *services:
    {{compose}} logs -f {{services}}

[group('stack')]
sh service="web":
    {{compose}} run --rm --no-deps {{service}} sh

# --- database --------------------------------------------------------------

[group('db')]
migrate:
    {{compose}} run --rm atlas migrate apply --env local

[group('db')]
migrate-diff name:
    {{compose}} run --rm atlas migrate diff {{name}} --env local

[group('db')]
migrate-lint:
    {{compose}} run --rm atlas migrate lint --env local --latest 1

[group('db')]
migrate-status:
    {{compose}} run --rm atlas migrate status --env local

[group('db')]
psql:
    {{compose}} exec postgres psql -U ${POSTGRES_USER:-couchcast} -d ${POSTGRES_DB:-couchcast}

# --- development -----------------------------------------------------------

[group('dev')]
server:
    {{compose}} up web

[group('dev')]
ingest:
    {{compose}} up ingest

[group('dev')]
web:
    {{compose}} up frontend

[group('dev')]
dev:
    {{compose}} up web ingest frontend

# --- code ------------------------------------------------------------------

# Packages run serially (-p 1): integration tests share one database.
[group('code')]
test *args:
    {{compose}} run --rm web go test -p 1 ./... {{args}}

[group('code')]
lint:
    {{compose}} run --rm --no-deps web golangci-lint run

[group('code')]
fmt:
    {{compose}} run --rm --no-deps web gofmt -l -w .

[group('code')]
tidy:
    {{compose}} run --rm --no-deps web go mod tidy

[group('code')]
pnpm *args:
    {{compose}} run --rm --no-deps frontend sh -c "corepack enable && pnpm {{args}}"

[group('code')]
check:
    just pnpm install
    just pnpm check

# --- build & production ----------------------------------------------------

[group('build')]
build tag=env("TAG", "dev"):
    docker build --target runtime -t {{image}}:{{tag}} .

[group('build')]
prod-up:
    {{prod}} up -d

[group('build')]
prod-down:
    {{prod}} down

[group('build')]
prod-migrate:
    {{prod}} run --rm atlas migrate apply --env local

# --- admin -----------------------------------------------------------------

[group('admin')]
admin-grant username:
    {{compose}} run --rm web go run ./cmd/couchcast admin grant {{username}}
