# API Documentation
<sup>*Last Updated: 2026-07-04*</sup>

The API is read-only and returns normalized metadata scraped from publicly accessible Arma Reforger Workshop pages.

Use `/v1` for all new integrations. The older unversioned routes still work as deprecated aliases for now.

## Health

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/health</code></div>

Returns process health only. It does not scrape the Workshop.

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
curl https://api.example.com/v1/mods/2?search=radio&sort=newest
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
    "apiModURL": "https://api.example.com/v1/mod/{mod_id}",
    "size": "192.42 KB",
    "rating": "92%",
    "ID": "{mod_id}"
  }],
  "links": {
    "next": "https://api.example.com/v1/mods/3?search=radio&sort=newest",
    "prev": "https://api.example.com/v1/mods/1?search=radio&sort=newest"
  }
}
```

## Get Mod

<div class="api-endpoint"><span class="api-method api-method-get">GET</span><code>/v1/mod/{mod_id}</code></div>

Example:

```bash
curl https://api.example.com/v1/mod/12345
```

Example response:

```json
{
  "status": "success",
  "mod": {
    "name": "Example Mod",
    "author": "Example Author",
    "originalModURL": "https://reforger.armaplatform.com/workshop/12345",
    "apiModURL": "https://api.example.com/v1/mod/12345",
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

Common codes: `INVALID_PAGE`, `INVALID_MOD_ID`, `INVALID_SEARCH`, `NOT_FOUND`, `RATE_LIMITED`, `QUERY_TOO_LONG`, `UPSTREAM_UNAVAILABLE`.

## Rate Limits & Cache

Anonymous clients are rate limited to **60 requests per minute** per resolved IP, with a burst of 20. `429` responses include `Retry-After` and rate-limit headers.

Responses may be cached and temporarily stale. Default cache windows:

| Resource | Fresh | Stale fallback |
| --- | --- | --- |
| Mod detail | 1 hour | 24 hours |
| List / search | 10 minutes | 1 hour |
| Not found | 10 minutes | — |

The API returns `Cache-Control`, `ETag`, and `X-Cache` headers. Workshop fields and page layout are controlled by Bohemia Interactive and may change upstream.
