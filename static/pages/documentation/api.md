# API Documentation
<sup>*Last Updated: 2026-07-07*</sup>

The API is read-only and returns normalized metadata scraped from publicly accessible Arma Reforger Workshop pages. It is intended for developers building Arma Reforger mod browsers, server dashboards, launchers, community tools, and other integrations that need Workshop mod data as JSON.

Use Reforger Mods API when you need to search Arma Reforger mods, fetch Workshop mod details, inspect dependencies, read scenario metadata, or link users back to official Arma Reforger Workshop pages.

Public API base URL: `https://api.reforgermods.net`

Use `/v1` for all new integrations. The older unversioned routes still work as deprecated aliases for now.

## Health

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/health</code></div>

Returns process health only. It does not scrape the Workshop.

## Refresh Jobs

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/refresh/jobs/{id}</code></div>

When a cacheable resource is missing from cache, the API queues a background refresh and returns `202 Accepted` instead of waiting for a slow Workshop scrape. Use the `Location` header to poll this endpoint, or retry the original resource URL after `Retry-After`.

Example job response:

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

Statuses are `queued`, `running`, `succeeded`, `failed`, and `expired`. Job responses do not include the full mod payload. After a job succeeds, request the original `resource_url`.

## List Mods

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/mods</code></div>
<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/mods/{page}</code></div>
<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/search?search={query}</code></div>

The Workshop currently returns 16 mods per page. Search and sort are passed through to the public Workshop page after normalization.

Query parameters:

| Name | Values |
| --- | --- |
| `search` | Search text, normalized and capped before use |
| `sort` | `popularity`, `newest`, `subscribers`, `version_size` |

Example:

```bash
curl https://api.reforgermods.net/v1/mods/2?search=radio&sort=newest
```

Example response:

```json
{
  "status": "success",
  "meta": {
    "totalPages": 2593,
    "currentPage": 2,
    "totalMods": 41478,
    "shownMods": 16,
    "modsIndexStart": 17,
    "modsIndexEnd": 32
  },
  "data": [{
    "name": "Example Mod",
    "author": "Example Author",
    "imageURL": "https://example.com/image.png",
    "originalModURL": "https://reforger.armaplatform.com/workshop/{mod_id}",
    "apiModURL": "https://api.reforgermods.net/v1/mod/{mod_id}",
    "size": "192.42 KB",
    "rating": "92%",
    "ID": "{mod_id}"
  }],
  "links": {
    "next": "https://api.reforgermods.net/v1/mods/3?search=radio&sort=newest",
    "prev": "https://api.reforgermods.net/v1/mods/1?search=radio&sort=newest"
  }
}
```

## Get Mod

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/mod/{mod_id}</code></div>

Example:

```bash
curl https://api.reforgermods.net/v1/mod/12345
```

Example response:

```json
{
  "status": "success",
  "mod": {
    "name": "Example Mod",
    "author": "Example Author",
    "originalModURL": "https://reforger.armaplatform.com/workshop/12345",
    "apiModURL": "https://api.reforgermods.net/v1/mod/12345",
    "imageURL": "https://example.com/image.png",
    "rating": "92%",
    "version": "1.1.0",
    "gameVersion": "1.1.0.34",
    "size": "192.42 KB",
    "subscribers": 0,
    "downloads": 791142,
    "created": "19.05.2022",
    "lastModified": "17.03.2024",
    "id": "12345",
    "summary": "Short Workshop summary",
    "description": "Workshop description",
    "license": "Arma Public License (APL)",
    "tags": ["EXAMPLE"],
    "dependencies": [],
    "scenarios": []
  }
}
```

## Errors

```json
{
  "error": {
    "code": "RATE_LIMITED",
    "message": "Too many requests.",
    "requestId": "..."
  }
}
```

Common codes: `INVALID_PAGE`, `INVALID_MOD_ID`, `INVALID_SEARCH`, `NOT_FOUND`, `RATE_LIMITED`, `QUERY_TOO_LONG`, `REFRESH_JOB_NOT_FOUND`, `REFRESH_QUEUE_FULL`, `REFRESH_SHUTTING_DOWN`, `UPSTREAM_UNAVAILABLE`.

Every API response includes an `X-Request-Id` header. Error responses also include the same value as `error.requestId`. Include that request ID when reporting an issue so the request can be found in server logs.

## Rate Limits & Cache

Anonymous clients are rate limited to **60 requests per minute** per resolved IP, with a burst of 20. `429` responses include `Retry-After` and rate-limit headers.

Responses may be cached and temporarily stale. The API is not guaranteed to be real-time. Default cache windows:

| Resource | Fresh | Stale fallback |
| --- | --- | --- |
| Mod detail | 1 hour | 24 hours |
| List / search | 10 minutes | 1 hour |
| Not found | 10 minutes | — |

The API does not expose a public cache-bypass flag. Refreshes are coalesced by normalized resource key, so repeated requests for the same cold or stale resource reuse one queued/running job instead of forcing repeated upstream scrapes.

Cache behavior:

| State | Response |
| --- | --- |
| Fresh cached data | `200 OK` with `X-Cache: HIT` |
| Stale but serveable cached data | `200 OK` with `X-Cache: STALE`, stale body returned immediately, one background refresh queued |
| Cold cache / missing cache data | `202 Accepted` with `Location`, `Retry-After`, `Cache-Control: no-store`, and a job status body |
| Refresh queue full | `503 Service Unavailable` with `Retry-After` |

Clients should honour `Retry-After`, `ETag`, and `Cache-Control`. For `202`, either poll the job URL from `Location` or retry the original resource URL after the suggested delay.

Cache headers:

| Header | Meaning |
| --- | --- |
| `X-Cache` | `MISS`, `HIT`, or `STALE` |
| `X-Refresh-Status` | `none`, `queued`, `running`, or `failed` for the related refresh |
| `X-Refresh-Job-Id` | Opaque job ID when a refresh job is associated with the response |
| `X-Refresh-Failed-At` | UTC timestamp when the latest refresh failed while stale data remains serveable |
| `Location` | Job status URL on `202 Accepted` |
| `Retry-After` | Suggested retry delay for `202`, queue saturation, and rate limiting |
| `Age` | Standard cache age in seconds |
| `X-Cache-Age` | Same cache age in seconds, included for easier client parsing |
| `X-Cache-Created-At` | UTC time when the cached response was stored |
| `X-Cache-Expires-At` | UTC time when the response stops being fresh |
| `X-Cache-Fresh-Seconds` | Seconds until the response stops being fresh |
| `X-Cache-Stale-At` | UTC time after which the cached response will no longer be served |
| `X-Cache-Stale-Seconds` | Seconds until the cached response becomes unusable |
| `Cache-Control` | Browser/proxy cache hint for the response |
| `ETag` | Weak validator for conditional requests |

When `X-Cache` is `STALE`, the API serves the cached response and starts a background refresh if one is not already queued or running. If that refresh fails, stale data remains available until the stale-serving window ends, and the response includes failure metadata without exposing raw scraper errors.

Cold-cache example:

```bash
curl -i 'https://api.reforgermods.net/v1/mods?search=radio'
```

```bash
HTTP/1.1 202 Accepted
Location: /v1/refresh/jobs/9f0b7d0f6fd4f88a8bb0e455f0b640247a93
Retry-After: 2
Cache-Control: no-store
X-Cache: MISS
X-Refresh-Status: queued
```

Stale response example:

```bash
HTTP/1.1 200 OK
X-Cache: STALE
X-Refresh-Status: queued
X-Refresh-Job-Id: 9f0b7d0f6fd4f88a8bb0e455f0b640247a93
```

Saturation example:

```bash
HTTP/1.1 503 Service Unavailable
Retry-After: 2
Cache-Control: no-store
X-Cache: MISS
X-Refresh-Status: failed
```

Refresh job state is process-local. In a multi-instance deployment, use sticky routing for job polling or add a shared job store in a future version.
