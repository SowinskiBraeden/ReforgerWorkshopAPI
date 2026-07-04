# API Documentation
<sup>*Last Updated: 2026-07-04*</sup>

The API is read-only and returns normalized metadata scraped from publicly accessible Arma Reforger Workshop pages. This project is independent and unofficial; it is not affiliated with or endorsed by Bohemia Interactive.

Use `/v1` for all new integrations. The older unversioned routes still work as deprecated aliases for now.

## Health

```http
GET /v1/health
```

Returns process health only. It does not scrape the Workshop.

## List Mods

```http
GET /v1/mods
GET /v1/mods/{page}
GET /v1/search?search={query}
```

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

```http
GET /v1/mod/{mod_id}
```

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

Common codes include `INVALID_PAGE`, `INVALID_MOD_ID`, `INVALID_SEARCH`, `NOT_FOUND`, `RATE_LIMITED`, `QUERY_TOO_LONG`, and `UPSTREAM_UNAVAILABLE`.

## Rate Limits and Cache

Anonymous clients are rate limited. The default public limit is 60 requests per minute per resolved client IP with a burst of 20. `429` responses include `Retry-After` and rate-limit headers.

Responses may be cached and temporarily stale. Default cache windows are 1 hour fresh plus 24 hours stale for mod details, 10 minutes fresh plus 1 hour stale for list/search responses, and 10 minutes for not-found responses. The API returns `Cache-Control`, `ETag`, and `X-Cache` headers.

Workshop fields and page layout are controlled by Bohemia Interactive and may change upstream.
