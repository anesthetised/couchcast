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
    {{compose}} up -d postgres seaweedfs

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
    {{compose}} run --rm web go test -p 1 -race ./... {{args}}

# Go test coverage in total over the product (cmd, internal, web: not the
# dev programs under tools/ and e2e/), counting code any package's tests
# reach (-coverpkg), as the CI badge does; `go tool cover -func coverage.out`
# (or -html) drills down.
[group('code')]
cover:
    {{compose}} run --rm web sh -c 'go test -p 1 -coverpkg=./cmd/...,./internal/...,./web -coverprofile=coverage.out ./... | grep -v "no test files" && go tool cover -func=coverage.out | tail -1'

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

# Frontend unit tests (Vitest); `just test-web --coverage` adds a report.
[group('code')]
test-web *args:
    just pnpm install
    just pnpm test {{args}}

# End-to-end browser tests (Playwright) against a fresh couchcast_e2e
# database and their own server and Vite instance; args go to
# `playwright test` (e.g. `just e2e tests/chat.spec.ts --headed` is not
# available in a container, use `--debug` locally instead).
[group('code')]
e2e *args:
    @# CI builds the dev image beforehand with a layer cache.
    @if [ -z "${E2E_PREBUILT:-}" ]; then {{compose}} build web; fi
    @# E2E_BUNDLE=1 tests the built bundle as the Go server serves it (with
    @# its security headers) instead of the Vite dev server.
    @if [ -n "${E2E_BUNDLE:-}" ]; then just pnpm install --frozen-lockfile && just pnpm build --emptyOutDir false; fi
    {{compose}} up -d --wait postgres seaweedfs
    {{e2e}} stop e2e-web e2e-frontend
    {{compose}} exec -T postgres psql -q -U ${POSTGRES_USER:-couchcast} -d postgres -c 'DROP DATABASE IF EXISTS couchcast_e2e WITH (FORCE)' -c 'CREATE DATABASE couchcast_e2e'
    {{compose}} run --rm -e COUCHCAST_DATABASE_URL={{e2e_db}} atlas migrate apply --env local
    {{e2e}} up -d --wait e2e-web e2e-frontend
    {{e2e}} exec -T e2e-web go run ./e2e/seed
    {{e2e}} run --rm ${E2E_BUNDLE:+-e BASE_URL=http://e2e-web:8080} e2e sh -c "corepack enable && pnpm install --frozen-lockfile && pnpm exec playwright test {{args}}"; status=$?; {{e2e}} rm -sf e2e-web e2e-frontend; if [ -n "${E2E_BUNDLE:-}" ]; then find web/dist -mindepth 1 ! -name .gitkeep -delete; fi; exit $status

# --- build & production ----------------------------------------------------

[group('build')]
build tag=env("TAG", "dev"):
    docker build --target runtime --build-arg VERSION={{tag}} -t {{image}}:{{tag}} .

# git-cliff renders CHANGELOG.md and the release notes from cliff.toml.
cliff := "orhunp/git-cliff:2.14.2"

# vYYYY.M.N: N counts releases within the month. Nothing is pushed:
# pushing the tag makes .github/workflows/release.yml publish the image
# and the GitHub release once CI has passed on that commit.
#
# Tag the next release with its CHANGELOG.md in a release commit.
[group('build')]
release:
    #!/bin/sh
    set -eu
    [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "releases are cut from main"; exit 1; }
    [ -z "$(git status --porcelain)" ] || { echo "the working tree has changes; commit or stash them first"; exit 1; }
    git fetch -q --tags origin main
    [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || { echo "main differs from origin/main; push or pull first"; exit 1; }
    year=$(date -u +%Y); month=$(date -u +%m); month=${month#0}
    n=0
    while git rev-parse -q --verify "refs/tags/v$year.$month.$n" >/dev/null; do n=$((n + 1)); done
    version="v$year.$month.$n"
    docker run --rm -v "$PWD:/app" -w /app {{cliff}} --tag "$version" -o CHANGELOG.md 2>/dev/null
    git add CHANGELOG.md
    git commit -q -m "chore(release): $version"
    git tag -a "$version" -m "couchcast $version"
    echo "tagged $version; publish it with: git push origin main $version"

# Checks out the release's tag (so compose files and migrations match the
# image), pulls the image, migrates, starts (behind Caddy when
# COUCHCAST_DOMAIN is set) and records TAG in .env for later compose
# commands. On failure the checkout goes back and .env keeps the old
# version; after migrations started, fix and deploy again.
#
# Deploy a published release on the server.
[group('build')]
deploy version:
    #!/bin/sh
    set -eu
    version="{{version}}"; version="${version#v}"
    [ -z "$(git status --porcelain --untracked-files=no)" ] || { echo "the checkout has local changes; deploy needs a clean one"; exit 1; }
    git fetch -q --tags origin
    git rev-parse -q --verify "refs/tags/v$version" >/dev/null || { echo "no release v$version"; exit 1; }
    previous=$(git symbolic-ref -q --short HEAD || git rev-parse HEAD)
    running="${TAG:-}"
    changed=0
    trap 'git checkout -q "$previous"; if [ $changed = 1 ]; then echo "deploy of $version failed; .env still says ${running:-no version}, but the database and containers may be on $version: fix and deploy again"; else echo "deploy of $version stopped; nothing was changed"; fi' EXIT
    git checkout -q "v$version"
    # Atlas accepts an older migration directory without complaint, so an
    # older release would run against a newer schema: refuse that.
    {{prod}} up -d --wait postgres >/dev/null 2>&1
    psql() { {{prod}} exec -T postgres psql -tA -U "${POSTGRES_USER:-couchcast}" -d "${POSTGRES_DB:-couchcast}" -c "$1"; }
    applied=""
    if [ "$(psql "SELECT to_regclass('atlas_schema_revisions.atlas_schema_revisions') IS NOT NULL")" = t ]; then
        applied=$(psql "SELECT max(version) FROM atlas_schema_revisions.atlas_schema_revisions")
    fi
    if [ -n "$applied" ] && ! ls db/migrations/"${applied}"_*.sql >/dev/null 2>&1; then
        echo "the database already has migration $applied, which $version does not know: its server would run"
        echo "against a newer schema. Restore a backup from before that migration (just restore), or deploy a newer release."
        exit 1
    fi
    export TAG="$version"
    # A release tag never changes, so an image already here is reused: a
    # rollback works even when the registry does not answer.
    {{prod}} pull --policy missing web ingest
    changed=1
    {{prod}} run --rm atlas migrate apply --env local
    if [ -n "${COUCHCAST_DOMAIN:-}" ]; then profile="--profile https"; else profile=""; fi
    {{prod}} $profile up -d --wait
    trap - EXIT
    touch .env
    if grep -q '^TAG=' .env; then sed -i.bak "s/^TAG=.*/TAG=$version/" .env && rm .env.bak; else echo "TAG=$version" >> .env; fi
    echo "couchcast $version is running"

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

# Load test against the running dev stack (`just dev`); see docs/load.md.
# Flags: -viewers 300 -stuck 10 -seeks 60 -every 200ms.
[group('code')]
load *args:
    {{compose}} run --rm web go run ./tools/loadtest {{args}}

# --- backups -----------------------------------------------------------------

# The compose project; its volumes are <project>_pgdata and <project>_s3data.
project := env("COMPOSE_PROJECT_NAME", "couchcast")

# The SeaweedFS volume is paused while it is archived, so its files agree.
#
# Back up the database and object storage into backups/<UTC time>/.
[group('ops')]
backup:
    #!/bin/sh
    set -eu
    dir="backups/$(date -u +%Y%m%dT%H%M%SZ)"
    mkdir -p "$dir"
    {{prod}} exec -T postgres pg_dump -U "${POSTGRES_USER:-couchcast}" -d "${POSTGRES_DB:-couchcast}" -Fc > "$dir/postgres.dump"
    {{prod}} pause seaweedfs
    trap '{{prod}} unpause seaweedfs' EXIT
    docker run --rm -v "{{project}}_s3data:/data:ro" -v "$PWD/$dir:/backup" alpine:3.23 tar -czf /backup/s3data.tar.gz -C /data .
    {{prod}} unpause seaweedfs
    trap - EXIT
    echo "backup written to $dir ($(du -sh "$dir" | cut -f1))"

# Restore a backup made by `just backup`; replaces the current data.
[confirm("Replace the database and object storage with this backup? Current data is lost.")]
[group('ops')]
restore dir:
    #!/bin/sh
    set -eu
    test -f "{{dir}}/postgres.dump" || { echo "no postgres.dump in {{dir}}"; exit 1; }
    {{prod}} stop web ingest
    {{prod}} exec -T postgres pg_restore -U "${POSTGRES_USER:-couchcast}" -d "${POSTGRES_DB:-couchcast}" --clean --if-exists --no-owner < "{{dir}}/postgres.dump"
    if [ -f "{{dir}}/s3data.tar.gz" ]; then
        {{prod}} stop seaweedfs
        docker run --rm -v "{{project}}_s3data:/data" -v "$PWD/{{dir}}:/backup:ro" alpine:3.23 \
            sh -c 'find /data -mindepth 1 -delete && tar -xzf /backup/s3data.tar.gz -C /data'
        # Wait until it serves reads again before the apps come back.
        {{prod}} up -d --wait seaweedfs
    fi
    {{prod}} start web ingest
    echo "restored from {{dir}}"

# Prove backups by restoring one: in a throwaway compose project, write a
# row and an object, back up, change both, restore, and compare.
[group('ops')]
backup-check:
    #!/bin/sh
    set -eu
    export COMPOSE_PROJECT_NAME=couchcast-backup-check
    dc="docker compose -f compose.yaml"
    dir=""
    cleanup() { $dc down -v >/dev/null 2>&1 || true; [ -n "$dir" ] && rm -rf "$dir"; rmdir backups 2>/dev/null || true; }
    trap cleanup EXIT
    sql() { $dc exec -T postgres psql -q -tA -U "${POSTGRES_USER:-couchcast}" -d "${POSTGRES_DB:-couchcast}" -c "$1"; }
    put() { $dc exec -T seaweedfs sh -c "echo $2 > /tmp/f && curl -sf -F file=@/tmp/f http://127.0.0.1:8888/buckets/couchcast/$1 >/dev/null"; }
    get() { $dc exec -T seaweedfs curl -sf "http://127.0.0.1:8888/buckets/couchcast/$1" || echo "(missing)"; }
    $dc up -d --wait postgres seaweedfs >/dev/null 2>&1
    sql "CREATE TABLE backup_check (v text); INSERT INTO backup_check VALUES ('kept')"
    put keep.txt kept
    dir=$(just backup | sed -n 's/^backup written to \([^ ]*\).*/\1/p')
    sql "UPDATE backup_check SET v = 'changed'"
    $dc exec -T seaweedfs curl -sf -X DELETE http://127.0.0.1:8888/buckets/couchcast/keep.txt >/dev/null
    put new.txt new
    just --yes restore "$dir" >/dev/null
    row=$(sql "SELECT v FROM backup_check"); keep=$(get keep.txt); new=$(get new.txt)
    echo "row: $row, keep.txt: $keep, new.txt: $new"
    [ "$row" = kept ] && [ "$keep" = kept ] && [ "$new" = "(missing)" ] || { echo "backup check failed"; exit 1; }
    echo "backup check passed"
