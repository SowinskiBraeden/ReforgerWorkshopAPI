# API Documentation

<sup>*Last Updated: 2026-07-09*</sup>

Reforger Mods API is a read-only API for Arma Reforger Workshop metadata.

It is built for mod browsers, server dashboards, launchers, Discord bots, and community tools that need Workshop data as JSON without scraping Workshop pages themselves.

The API is independent and unofficial. It is not affiliated with or endorsed by Bohemia Interactive.

## Base URL

```text
https://api.reforgermods.net
```

Use `/v1` for all new integrations.

```text
GET /v1/health
GET /v1/mods
GET /v1/mods/{page}
GET /v1/search?search={query}
GET /v1/mod/{mod_id}
GET /v1/refresh/jobs/{id}
```

Older unversioned routes remain available as deprecated aliases for now. New integrations should always use `/v1`.

## Quick Start

Request an endpoint normally.

```bash
curl 'https://api.reforgermods.net/v1/mods?search=radio'
```

Most requests return `200 OK` with JSON data.

For an uncached resource, the API may return `202 Accepted` while it fetches current Workshop data. In that case:

1. Read `Retry-After`.
2. Wait for that delay.
3. Retry the original request URL.

Polling the refresh-job URL from `Location` is optional. It is mainly useful when your application wants to show loading progress.

The [202 and refresh jobs guide](/guides/handling-202-refresh-jobs/) covers this flow in more depth, and the [integration guide](/guides/api-integration/) walks through building a complete client.

## Identify Your Client

Send an identifying `User-Agent` or `X-API-Client` header with every request. Include your project name, a version, and a way to reach you:

```text
User-Agent: my-server-panel/2.1 (+https://example.com; admin@example.com)
```

or

```text
X-API-Client: my-server-panel/2.1
```

Identified traffic is easier to support, easier to attribute in metrics, and will not be mistaken for an abusive scraper.

## Health

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/health</code></div>

Returns process health only. It does not request Workshop data.

```bash
curl https://api.reforgermods.net/v1/health
```

```json
{
  "status": "success",
  "data": {
    "code": 200,
    "alive": true
  }
}
```

## List Mods

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/mods</code></div>
<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/mods/{page}</code></div>

`/v1/mods` returns the first page of Workshop listings.

`/v1/mods/{page}` returns a specific page. Pages must be positive integers.

### Query Parameters

| Name     | Values                                                   |
| -------- | -------------------------------------------------------- |
| `search` | Optional search text                                     |
| `sort`   | `popularity`, `newest`, `subscribers`, or `version_size` |

Example:

```bash
curl 'https://api.reforgermods.net/v1/mods/2?search=radio&sort=newest'
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
  "data": [
    {
      "name": "Example Mod",
      "author": "Example Author",
      "imageURL": "https://example.com/image.png",
      "originalModURL": "https://reforger.armaplatform.com/workshop/5965550F24A0C152",
      "apiModURL": "https://api.reforgermods.net/v1/mod/5965550F24A0C152",
      "size": "192.42 KB",
      "rating": "92%",
      "ID": "5965550F24A0C152"
    }
  ],
  "links": {
    "next": "https://api.reforgermods.net/v1/mods/3?search=radio&sort=newest",
    "prev": "https://api.reforgermods.net/v1/mods/1?search=radio&sort=newest"
  }
}
```

## Search Mods

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/search?search={query}</code></div>

Search is a convenience route for first-page results.

```bash
curl 'https://api.reforgermods.net/v1/search?search=radio'
```

It returns the same response shape as:

```text
GET /v1/mods?search=radio
```

## Get Mod Details

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/mod/{mod_id}</code></div>

Returns detailed metadata for one Workshop mod.

```bash
curl 'https://api.reforgermods.net/v1/mod/5965550F24A0C152'
```

Example response:

```json
{
  "status": "success",
  "data": {
    "name": "Example Mod",
    "author": "Example Author",
    "originalModURL": "https://reforger.armaplatform.com/workshop/5965550F24A0C152",
    "apiModURL": "https://api.reforgermods.net/v1/mod/5965550F24A0C152",
    "imageURL": "https://example.com/image.png",
    "rating": "92%",
    "version": "1.1.0",
    "gameVersion": "1.1.0.34",
    "size": "192.42 KB",
    "subscribers": 0,
    "downloads": 791142,
    "created": "19.05.2022",
    "lastModified": "17.03.2024",
    "id": "5965550F24A0C152",
    "summary": "Short Workshop summary",
    "description": "Workshop description",
    "license": "Arma Public License (APL)",
    "tags": [
      "EXAMPLE"
    ],
    "dependencies": [
      {
        "name": "Example Dependency",
        "originalModURL": "https://reforger.armaplatform.com/workshop/example-dependency-id",
        "apiModURL": "https://api.reforgermods.net/v1/mod/example-dependency-id"
      }
    ],
    "scenarios": [
      {
        "name": "Example Scenario",
        "description": "Scenario summary",
        "scenarioID": "{EXAMPLE}Missions/Example.conf",
        "gamemode": "Game Master",
        "playerCount": 16,
        "imageURL": "https://example.com/scenario-image.png"
      }
    ]
  }
}
```

`dependencies`, `scenarios`, tags, and optional metadata may be empty when the Workshop does not provide them.

## Refresh Jobs

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/refresh/jobs/{id}</code></div>

Most integrations do not need to call this endpoint.

When a resource is not available in cache, the API may return:

```text
HTTP/1.1 202 Accepted
Location: /v1/refresh/jobs/9f0b7d0f6fd4f88a8bb0e455f0b640247a93
Retry-After: 2
X-Cache: MISS
```

The simplest handling is to wait for `Retry-After`, then retry the original URL.

For applications that want to show progress, request the URL in `Location`:

```bash
curl 'https://api.reforgermods.net/v1/refresh/jobs/9f0b7d0f6fd4f88a8bb0e455f0b640247a93'
```

```json
{
  "id": "9f0b7d0f6fd4f88a8bb0e455f0b640247a93",
  "status": "queued",
  "resource_url": "/v1/mods?search=radio",
  "retry_after_seconds": 2
}
```

Possible statuses are `queued`, `running`, `succeeded`, `failed`, and `expired`.

After a job succeeds, request `resource_url` again. Refresh-job responses do not contain the finished mod or list payload.

## Cache and Rate Limits

Workshop data is cached and may be temporarily stale. This allows the API to remain responsive when the upstream Workshop is slow or unavailable.

| Resource            | Fresh cache | Stale fallback |
| ------------------- | ----------- | -------------- |
| Mod detail          | 1 hour      | 24 hours       |
| Mod list and search | 10 minutes  | 1 hour         |
| Not found response  | 10 minutes  | —              |

Useful response headers:

| Header         | Meaning                                                 |
| -------------- | ------------------------------------------------------- |
| `X-Cache`      | `HIT`, `STALE`, or `MISS`                               |
| `Retry-After`  | How long to wait before retrying                        |
| `Location`     | Refresh-job URL on `202 Accepted`                       |
| `ETag`         | Optional validator for client-side conditional requests |
| `X-Request-Id` | Request identifier for support and debugging            |

There is no public cache-bypass or force-refresh parameter.

Anonymous clients are limited to **60 requests per minute** per resolved client IP, with a burst allowance of 20.

For `429 Too Many Requests` or `503 Service Unavailable`, wait for `Retry-After` and retry later.

Do not poll refresh jobs faster than the advertised delay or repeatedly request variations of the same query to bypass caching.

### Conditional Requests

Responses include an `ETag`. Send it back in `If-None-Match` to get a free `304 Not Modified` when nothing changed:

```bash
curl -H 'If-None-Match: W/"a1b2c3d4e5f6a7b8c9d0e1f2"' \
  'https://api.reforgermods.net/v1/mods?search=radio'
```

## Code Examples

Complete request handling — including `202 Accepted` retries — in curl and Python.

### curl

```bash
# Search mods (retry manually if you get 202 Accepted)
curl -H 'X-API-Client: my-app/1.0' \
  'https://api.reforgermods.net/v1/mods?search=radio&sort=newest'

# Fetch one mod's details
curl -H 'X-API-Client: my-app/1.0' \
  'https://api.reforgermods.net/v1/mod/5965550F24A0C152'
```

### Python

```python
import time
import requests

def get_json(url, attempts=4):
    headers = {"User-Agent": "my-app/1.0 (contact@example.com)"}
    for _ in range(attempts):
        response = requests.get(url, headers=headers, timeout=15)
        if response.status_code == 202:
            time.sleep(int(response.headers.get("Retry-After", "2")))
            continue
        response.raise_for_status()
        return response.json()
    raise RuntimeError("Workshop data is still refreshing")

results = get_json("https://api.reforgermods.net/v1/mods?search=radio")
mod = get_json("https://api.reforgermods.net/v1/mod/5965550F24A0C152")
```

## Errors

Errors use a consistent JSON response:

```json
{
  "error": {
    "code": "RATE_LIMITED",
    "message": "Too many requests.",
    "requestId": "..."
  }
}
```

Common error codes:

| Code                    | Meaning                                      |
| ----------------------- | -------------------------------------------- |
| `INVALID_PAGE`          | The requested page is invalid.               |
| `INVALID_MOD_ID`        | The mod ID is malformed.                     |
| `INVALID_SEARCH`        | The search text is invalid.                  |
| `NOT_FOUND`             | No matching resource was found.              |
| `RATE_LIMITED`          | Too many requests were sent.                 |
| `REFRESH_JOB_NOT_FOUND` | The refresh job no longer exists.            |
| `REFRESH_QUEUE_FULL`    | Refresh capacity is temporarily unavailable. |
| `REFRESH_SHUTTING_DOWN` | The service is shutting down.                |

Every response includes `X-Request-Id`. Include that value when reporting an issue.

## Data Source and Availability

Data is normalized from publicly accessible Arma Reforger Workshop pages.

Workshop data, fields, availability, and page structure can change without notice. The API is not an authoritative source for ownership, entitlement, moderation, platform accounts, or real-time Workshop state.

## See It in Action

The API powers the web tools on this site, so you can watch real request handling in your browser network tab:

* [Mod browser](/arma-reforger-mods/) — search, pagination, and 202 retries.
* [Config validator](/config-validator/) — mod ID resolution with only IDs sent.
* [Config generator](/config-generator/) and [mod manager](/mod-manager/) — building a server config.json from API data.
