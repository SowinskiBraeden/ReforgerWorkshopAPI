# Reforger Workshop API

Reforger Workshop API is a read-only API for discovering Arma Reforger Workshop mod metadata without making every downstream client scrape the public Workshop page directly.

This is a Cedarline product and an independent, unofficial project. It is not affiliated with or endorsed by Bohemia Interactive. Data is normalized and cached from publicly accessible Arma Reforger Workshop pages at `reforger.armaplatform.com/workshop`; upstream availability, fields, layout, sorting, and rate limits may change without notice.

Do not treat this API as an authoritative source for ownership, entitlement, moderation, identity, or platform account data. Cached responses may be stale by design. Permanent API stability is not guaranteed until versioned endpoints are explicitly stabilized.

## Current API

Versioned routes are available under `/v1`:

```text
GET /v1/health
GET /v1/mods
GET /v1/mods/{page}
GET /v1/mod/{id}
GET /v1/search?search={query}
```

The old unversioned routes are still present as deprecated aliases and return a `Deprecation: true` header plus a `Link` header pointing to the `/v1` route. New clients should use `/v1`.

Errors use a consistent envelope:

```json
{
  "error": {
    "code": "RATE_LIMITED",
    "message": "Too many requests.",
    "requestId": "..."
  }
}
```

## Rate Limiting

Anonymous public clients are rate limited by resolved client IP. Defaults:

```text
ANON_RATE_LIMIT_PER_MINUTE=60
ANON_RATE_BURST=20
```

`429 Too Many Requests` responses include `Retry-After`, `RateLimit-Limit`, `RateLimit-Remaining`, and `RateLimit-Reset` where practical.

Forwarded client headers are ignored unless the direct remote address is in `TRUSTED_PROXY_CIDRS`. Configure this when the service runs behind Cloudflare, Nginx, Caddy, or another trusted reverse proxy. Do not include broad internet ranges.

Example for a private reverse proxy hop:

```env
TRUSTED_PROXY_CIDRS=127.0.0.1/32,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
```

Cloudflare deployments should keep the API bound privately behind the proxy and set `TRUSTED_PROXY_CIDRS` to the Cloudflare source ranges that can reach the origin, plus any private proxy hop. Keep those ranges current from Cloudflare's published list.

The middleware is isolated around a rate-limit identity boundary so a future API-key layer can assign named buckets and higher quotas. API keys, billing, dashboards, and subscription tiers are not implemented.

## Cache Behavior

The service uses a bounded in-memory cache for the initial deployment. It caches successful responses and short-lived not-found responses, deduplicates concurrent cache misses for the same normalized key, and can serve stale data during refresh or upstream failure within the configured stale window.

Defaults:

```text
CACHE_MOD_TTL=1h
CACHE_MOD_STALE=24h
CACHE_LIST_TTL=10m
CACHE_LIST_STALE=1h
CACHE_NOT_FOUND_TTL=10m
CACHE_MAX_ENTRIES=1000
CACHE_REFRESH_CONCURRENCY=8
```

Cache keys include API version, route shape, mod ID, page, normalized search text, and sort. Search strings are normalized and capped before cache-key creation to limit cardinality.

Responses include `Cache-Control`, `ETag`, and `X-Cache` (`HIT`, `MISS`, or `STALE`). Users may not see Workshop updates immediately.

## Upstream Scraping

The scraper uses a service-identifying User-Agent, request timeouts, bounded retries for transient failures, exponential backoff with jitter, and global upstream concurrency limits. It does not scrape speculatively in the background; upstream fetches are driven by client requests and cache refreshes.

Defaults:

```text
UPSTREAM_TIMEOUT=15s
UPSTREAM_RETRIES=2
UPSTREAM_CONCURRENCY=4
UPSTREAM_USER_AGENT=Cedarline Reforger Workshop API/1.0 (+https://cedarline.digital)
```

## Configuration

Copy `.env.example` to `.env` for local development.

Important variables:

```text
PORT=8000
FULL_URL=http://localhost:8000
CORS_ALLOWED_ORIGINS=
TRUSTED_PROXY_CIDRS=
ANON_RATE_LIMIT_PER_MINUTE=60
ANON_RATE_BURST=20
CACHE_MOD_TTL=1h
CACHE_LIST_TTL=10m
UPSTREAM_TIMEOUT=15s
UPSTREAM_CONCURRENCY=4
```

CORS is not permissive by default. Set `CORS_ALLOWED_ORIGINS` to a comma-separated list of browser origins that should be allowed.

## Local Development

```bash
cp .env.example .env
go test ./...
go run .
curl http://localhost:8000/v1/health
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

## Deployment Notes

Recommended topology:

```text
Internet -> Cloudflare -> Nginx/Caddy on the host -> Reforger Workshop API on a private port
```

Bind the Go service privately where possible and expose only the reverse proxy publicly. Do not expose debug, profiling, metrics, or future admin/cache-purge endpoints publicly by default. No persistent storage is used by the current in-memory cache, so there is no backup requirement for cache data.

Health behavior:

```text
GET /v1/health
```

The health endpoint verifies the API process is alive. It does not scrape the Workshop and does not prove upstream availability.

The server uses read, write, idle, and read-header timeouts and shuts down gracefully on SIGINT/SIGTERM.

## Footer Attribution

© cedarline.digital Reforger Workshop API is an independent, unofficial project and is not affiliated with Bohemia Interactive.
