# Reforger Mods API

Reforger Mods API is an unofficial, read-only API and public documentation site for working with Arma Reforger Workshop mod metadata without making every downstream client scrape the public Workshop page directly.

This is a Cedarline product and an independent, unofficial project. It is not affiliated with or endorsed by Bohemia Interactive. Data is normalized and cached from publicly accessible Arma Reforger Workshop pages at `reforger.armaplatform.com/workshop`; upstream availability, fields, layout, sorting, and rate limits may change without notice.

Do not treat this API as an authoritative source for ownership, entitlement, moderation, identity, or platform account data. Cached responses may be stale by design. Permanent API stability is not guaranteed until versioned endpoints are explicitly stabilized.

## Current API

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

The service uses a bounded in-memory cache and a bounded background refresh queue for the initial deployment. Public API requests do not wait for slow Workshop scrapes.

Cache states:

- Fresh: cached data is inside its normal TTL. The API returns `200 OK` immediately with `X-Cache: HIT`.
- Stale but serveable: cached data is past the fresh TTL but still inside the stale-serving window. The API returns stale data immediately with `200 OK`, `X-Cache: STALE`, and queues one coalesced background refresh for that resource.
- Cold cache: no usable cached data exists. The API queues a refresh job and returns `202 Accepted` quickly with `X-Cache: MISS`, `Location`, `Retry-After`, and a compact job body.
- Saturated: if the refresh queue is full and no existing job can be reused, the API returns `503 Service Unavailable` with `Retry-After`.

Successful `200` response bodies keep the same shape as before. The `202` response means the client should poll the job endpoint or retry the original resource URL after `Retry-After`.

Defaults:

```text
CACHE_MOD_TTL=1h
CACHE_MOD_STALE=24h
CACHE_LIST_TTL=10m
CACHE_LIST_STALE=1h
CACHE_NOT_FOUND_TTL=10m
CACHE_MAX_ENTRIES=1000
CACHE_REFRESH_CONCURRENCY=8
CACHE_REFRESH_QUEUE_SIZE=64
CACHE_REFRESH_TIMEOUT=20s
CACHE_REFRESH_JOB_RETENTION=15m
CACHE_REFRESH_RETRY_AFTER=2s
```

Cache keys include API version, route shape, mod ID, page, normalized search text, and sort. Search strings are normalized and capped before cache-key creation to limit cardinality.

Responses include `Cache-Control`, `ETag`, and `X-Cache` (`HIT`, `MISS`, or `STALE`). Users may not see Workshop updates immediately. Public clients cannot force repeated upstream scraping; refreshes for the same normalized resource are coalesced while queued or running.

Freshness is also exposed in response headers:

```text
Age
ETag
Cache-Control
Location
Retry-After
X-Cache
X-Cache-Age
X-Cache-Created-At
X-Cache-Expires-At
X-Cache-Fresh-Seconds
X-Cache-Stale-At
X-Cache-Stale-Seconds
X-Refresh-Status
X-Refresh-Job-Id
X-Refresh-Failed-At
```

`Age` and `X-Cache-Age` show how many seconds old the cached response is. `X-Cache-Expires-At` and `X-Cache-Stale-At` show when the response stops being fresh and when it stops being serveable. Public clients cannot force a cache bypass; stale entries refresh in the background where possible.

`X-Refresh-Status` is `none`, `queued`, `running`, or `failed` on resource responses. `X-Refresh-Failed-At` is included when the latest refresh failed but stale data remains serveable. Raw scraper errors and upstream URLs are not exposed.

Example cold-cache response:

```bash
curl -i https://api.reforgermods.net/v1/mods?search=radio
```

```http
HTTP/1.1 202 Accepted
Location: /v1/refresh/jobs/9f0b7d0f6fd4f88a8bb0e455f0b640247a93
Retry-After: 2
Cache-Control: no-store
X-Cache: MISS
X-Refresh-Status: queued
```

```json
{
  "id": "9f0b7d0f6fd4f88a8bb0e455f0b640247a93",
  "status": "queued",
  "resource_url": "/v1/mods?search=radio",
  "created_at": "2026-07-07T20:00:00Z",
  "updated_at": "2026-07-07T20:00:00Z",
  "retry_after_seconds": 2
}
```

Poll the job:

```bash
curl https://api.reforgermods.net/v1/refresh/jobs/9f0b7d0f6fd4f88a8bb0e455f0b640247a93
```

Then retry the original resource URL. A successful refresh does not duplicate the mod payload in the job response.

Example stale response:

```http
HTTP/1.1 200 OK
X-Cache: STALE
X-Refresh-Status: queued
X-Refresh-Job-Id: 9f0b7d0f6fd4f88a8bb0e455f0b640247a93
Cache-Control: public, max-age=0, stale-while-revalidate=0, stale-if-error=3582
```

Example saturation response:

```http
HTTP/1.1 503 Service Unavailable
Retry-After: 2
Cache-Control: no-store
X-Cache: MISS
X-Refresh-Status: failed
```

Job state is in process memory. In multi-instance deployments, a job created on one instance is not visible from another unless traffic is sticky or a shared job store is added in a future release.

## Upstream Scraping

The scraper uses a service-identifying User-Agent, request timeouts, bounded retries for transient failures, exponential backoff with jitter, and global upstream concurrency limits. It does not scrape speculatively in the background; upstream fetches are driven by client requests and cache refreshes.

Defaults:

```text
UPSTREAM_TIMEOUT=15s
UPSTREAM_RETRIES=2
UPSTREAM_CONCURRENCY=4
UPSTREAM_USER_AGENT=Cedarline Reforger Mods API/1.0 (+https://cedarline.digital)
```

## Configuration

Copy `.env.example` to `.env` for local development.

Important variables:

```text
BIND_ADDRESS=0.0.0.0:8000
FULL_URL=https://api.reforgermods.net
PUBLIC_BASE_URL=https://reforgermods.net
API_BASE_URL=https://api.reforgermods.net
PUBLIC_CANONICAL_REDIRECTS=true
LOG_DIR=logs
LOG_TO_STDOUT=true
LOG_RETENTION_DAYS=14
CORS_ALLOWED_ORIGINS=
TRUSTED_PROXY_CIDRS=
ANON_RATE_LIMIT_PER_MINUTE=60
ANON_RATE_BURST=20
CACHE_MOD_TTL=1h
CACHE_LIST_TTL=10m
UPSTREAM_TIMEOUT=15s
UPSTREAM_CONCURRENCY=4
CACHE_REFRESH_CONCURRENCY=8
CACHE_REFRESH_QUEUE_SIZE=64
```

`FULL_URL` is retained as the legacy API origin fallback. New deployments should set `PUBLIC_BASE_URL` for public pages, canonical links, robots.txt, and sitemap.xml, and `API_BASE_URL` for machine API examples and generated API links. Set `PUBLIC_CANONICAL_REDIRECTS=true` only when the reverse proxy sends the original host and both public and API hostnames are routed correctly. Canonical redirects are applied only to public HTML routes, not API JSON routes.

CORS is not permissive by default. Set `CORS_ALLOWED_ORIGINS` to a comma-separated list of browser origins that should be allowed.

## Logging

Logs are structured JSON. By default they are written to stdout and to daily files:

```text
logs/2026-07-04.log
logs/2026-07-05.log
```

Useful settings:

```text
LOG_DIR=logs
LOG_TO_STDOUT=true
LOG_RETENTION_DAYS=14
```

Every API response includes an `X-Request-Id` header. Error responses include the same value in the JSON envelope. Request logs include `requestId`, `clientIP`, `method`, `path`, `query`, response `status`, latency, and user agent. Cache logs include `requestId`, cache key, and cache status.

Example lookup:

```bash
rg '"requestId":"1751659200000000000"' logs/
```

Clean old daily logs:

```bash
LOG_RETENTION_DAYS=14 ./scripts/clean-logs.sh
```

Set `LOG_DIR` when logs live somewhere other than `./logs`.

## Systemd

The recommended production path is a systemd-managed binary behind Caddy. A ready-to-use unit file lives at [deploy/reforger-mods-api.service](/home/flami/Projects/ReforgerWorkshopAPI/deploy/reforger-mods-api.service), with a deployment checklist in [deploy/README.md](/home/flami/Projects/ReforgerWorkshopAPI/deploy/README.md).

The basic release flow is:

```bash
go build -o /opt/reforgermods-api/releases/<release>/reforgermods-api .
ln -sfn /opt/reforgermods-api/releases/<release> /opt/reforgermods-api/current
sudo systemctl restart reforger-mods-api
```

The Dockerfile copies `static/` into the runtime image. Server-rendered public pages are compiled into the Go binary and reference lightweight static CSS and icon assets from that directory.

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
Internet -> Cloudflare -> Nginx/Caddy on the host -> Reforger Mods API on a private port
```

Preferred hostname split:

```text
https://reforgermods.net      -> public site and documentation
https://api.reforgermods.net  -> machine API endpoints
```

Bind the Go service privately where possible and expose only the reverse proxy publicly. Do not expose debug, profiling, metrics, or future admin/cache-purge endpoints publicly by default. No persistent storage is used by the current in-memory cache, so there is no backup requirement for cache data.

Public pages are served by the Go application at `/`, `/arma-reforger-mods/`, `/arma-reforger-mods-api/`, `/docs/`, `/docs/mod-structures/`, `/docs/methodology/`, `/docs/changelog/`, `/privacy/`, and `/terms/`. API endpoints include `X-Robots-Tag: noindex, nofollow, noarchive` so raw JSON does not compete with documentation pages in search results.

Health behavior:

```text
GET /v1/health
```

The health endpoint verifies the API process is alive. It does not scrape the Workshop and does not prove upstream availability.

The server uses read, write, idle, and read-header timeouts and shuts down gracefully on SIGINT/SIGTERM.

See [deploy/README.md](/home/flami/Projects/ReforgerWorkshopAPI/deploy/README.md) for the launch checklist, reverse-proxy notes, sitemap validation, and manual Search Console steps.

## Footer Attribution

© 2025-2026 reforgermods.net. Reforger Mods API is an independent, unofficial API service and is not affiliated with Bohemia Interactive. cedarline.digital is linked separately as the Cedarline Digital site.
