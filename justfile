# Every recipe runs inside containers; Docker and just are the only local
# requirements. `.env` is loaded automatically for compose substitutions.

set shell := ["sh", "-c"]
set dotenv-load := true

compose := "docker compose -f compose.yaml -f compose.dev.yaml"
e2e := "docker compose -f compose.yaml -f compose.dev.yaml -f compose.e2e.yaml --profile e2e"
e2e_db := "postgres://${POSTGRES_USER:-couchcast}:${POSTGRES_PASSWORD:-couchcast}@postgres:5432/couchcast_e2e?sslmode=disable"
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

# End-to-end browser tests (Playwright) against a fresh couchcast_e2e
# database and their own server and Vite instance; args go to
# `playwright test` (e.g. `just e2e tests/chat.spec.ts --headed` is not
# available in a container, use `--debug` locally instead).
[group('code')]
e2e *args:
    {{compose}} build web
    {{compose}} up -d postgres minio minio-init
    {{e2e}} stop e2e-web e2e-frontend
    {{compose}} exec -T postgres psql -q -U ${POSTGRES_USER:-couchcast} -d postgres -c 'DROP DATABASE IF EXISTS couchcast_e2e WITH (FORCE)' -c 'CREATE DATABASE couchcast_e2e'
    {{compose}} run --rm -e COUCHCAST_DATABASE_URL={{e2e_db}} atlas migrate apply --env local
    {{e2e}} up -d --wait e2e-web e2e-frontend
    {{e2e}} run --rm e2e sh -c "corepack enable && pnpm install --frozen-lockfile && pnpm exec playwright test {{args}}"; status=$?; {{e2e}} rm -sf e2e-web e2e-frontend; exit $status

# --- build & production ----------------------------------------------------

[group('build')]
build tag=env("TAG", "dev"):
    docker build --target runtime -t {{image}}:{{tag}} .

[group('build')]
prod-up:
    {{prod}} up -d

# Production behind Caddy with automatic HTTPS (needs COUCHCAST_DOMAIN).
[group('build')]
prod-https:
    @test -n "${COUCHCAST_DOMAIN:-}" || { echo "set COUCHCAST_DOMAIN in .env (the public host name)"; exit 1; }
    {{prod}} --profile https up -d

[group('build')]
prod-down:
    {{prod}} --profile https down

[group('build')]
prod-migrate:
    {{prod}} run --rm atlas migrate apply --env local

# --- admin -----------------------------------------------------------------

[group('admin')]
admin-grant username:
    {{compose}} run --rm web go run ./cmd/couchcast admin grant {{username}}
