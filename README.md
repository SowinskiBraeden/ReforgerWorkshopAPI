# Reforger Mods API

Reforger Mods API is an unofficial, read-only API and public documentation site for Arma Reforger Workshop mod metadata.

The program serves a small public website and JSON API from one Go service. It fetches publicly available Workshop pages, normalizes mod metadata, caches responses, and exposes stable API routes so tools do not need to scrape the Workshop directly.

This project is independent and is not affiliated with or endorsed by Bohemia Interactive. Do not treat the API as an authoritative source for ownership, entitlement, moderation, identity, or platform account data. Cached responses may be stale by design.

by [cedarline.digital](https://cedarline.digital)

## Official Public Deployment

Public site and documentation:

```text
https://reforgermods.net
```

Public API base URL:

```text
https://api.reforgermods.net
```

Versioned routes are available under `/v1`:

```text
GET /v1/health
GET /v1/mods
GET /v1/mods/{page}
GET /v1/mod/{id}
GET /v1/search?search={query}
GET /v1/refresh/jobs/{id}
```

Examples:

```bash
curl https://api.reforgermods.net/v1/health
curl https://api.reforgermods.net/v1/mods
curl 'https://api.reforgermods.net/v1/mods/2?search=radio&sort=newest'
curl https://api.reforgermods.net/v1/mod/12345
```

The old unversioned routes are still present as deprecated aliases. New clients should use `/v1`.

Error responses use this shape:

```json
{
  "error": {
    "code": "RATE_LIMITED",
    "message": "Too many requests.",
    "requestId": "..."
  }
}
```

## Website Tools

Besides the API and documentation, the website serves free browser tools for Arma Reforger server admins, all powered by the same API:

- `/arma-reforger-mods/` — searchable Workshop mod browser with mod detail pages
- `/config-generator/` — form-based server `config.json` builder with live preview and export
- `/config-validator/` — local, in-browser `config.json` validation with optional mod ID checks
- `/mod-manager/` — editor for the `game.mods` array with name resolution and dependency suggestions
- `/guides/` — server config and API integration guides

Config editing happens client-side; configs are never uploaded.

## Requirements

- Go 1.23 or newer
- Network access to `reforger.armaplatform.com`
- Optional: Docker or Docker Compose

## Configuration

Copy `.env.example` to `.env` before running locally:

```bash
cp .env.example .env
```

Common settings:

```text
BIND_ADDRESS=0.0.0.0:8000
PUBLIC_BASE_URL=http://localhost:8000
API_BASE_URL=http://localhost:8000
FULL_URL=http://localhost:8000
PUBLIC_CANONICAL_REDIRECTS=false
LOG_DIR=logs
LOG_TO_STDOUT=true
CORS_ALLOWED_ORIGINS=
TRUSTED_PROXY_CIDRS=
ANON_RATE_LIMIT_PER_MINUTE=60
ANON_RATE_BURST=20
```

`PUBLIC_BASE_URL` is used for public pages, canonical links, `robots.txt`, and `sitemap.xml`.

`API_BASE_URL` is used for API examples and generated API links.

`FULL_URL` is retained as a legacy API origin fallback.

`PUBLIC_CANONICAL_REDIRECTS` should usually stay `false` for local development.

`CORS_ALLOWED_ORIGINS` is empty by default. Set it to a comma-separated list of browser origins if cross-origin browser access is needed.

`TRUSTED_PROXY_CIDRS` controls which direct proxy IPs are allowed to provide forwarded client headers. Leave it empty for local direct access.

Cache and upstream defaults are also configured through `.env`:

```text
CACHE_MOD_TTL=1h
CACHE_MOD_STALE=24h
CACHE_LIST_TTL=10m
CACHE_LIST_STALE=1h
CACHE_MAX_ENTRIES=1000
CACHE_REFRESH_CONCURRENCY=8
CACHE_REFRESH_QUEUE_SIZE=64
UPSTREAM_TIMEOUT=15s
UPSTREAM_RETRIES=2
UPSTREAM_CONCURRENCY=4
```

## Build and Run

Run tests:

```bash
go test ./...
```

Run locally:

```bash
go run .
```

Then check:

```bash
curl http://localhost:8000/v1/health
```

Build a local binary:

```bash
go build -o reforger-workshop-api .
./reforger-workshop-api
```

## Docker

Build and run:

```bash
docker build -t reforger-workshop-api .
docker run --env-file .env -p 8000:8000 reforger-workshop-api
```

With Compose:

```bash
docker compose up --build
```

## Runtime Behavior

The API uses an in-memory cache and a bounded background refresh queue. Public API requests do not wait indefinitely for slow Workshop scrapes.

- Fresh cache entries return `200 OK` with `X-Cache: HIT`.
- Stale but usable entries return `200 OK` with `X-Cache: STALE` and queue a background refresh.
- Cold cache requests return `202 Accepted` with a refresh job location.
- Saturated refresh queues return `503 Service Unavailable` with `Retry-After`.

Every API response includes an `X-Request-Id` header. Error responses include the same value in the JSON body.

Logs are structured JSON and are written to stdout and daily files by default. Old daily logs can be cleaned with:

```bash
LOG_RETENTION_DAYS=14 ./scripts/clean-logs.sh
```
