# Deployment Checklist

Reforger Mods API is one Go service that serves both the public HTML site and the JSON API. The preferred production layout is:

```text
https://reforgermods.net      -> public site and documentation
https://api.reforgermods.net  -> machine API endpoints
```

If the current reverse proxy can only expose one hostname, keep both public pages and API routes on that host and set both base URL variables to the same origin.

## Required Environment

```env
PUBLIC_BASE_URL=https://reforgermods.net
API_BASE_URL=https://api.reforgermods.net
FULL_URL=https://api.reforgermods.net
PUBLIC_CANONICAL_REDIRECTS=true
INDEX_ENABLED=true
INDEX_DB_PATH=/var/lib/reforgermods-api/reforgermods-index.db
INDEX_REFRESH_ENABLED=true
```

`FULL_URL` remains a legacy fallback for generated API links. `PUBLIC_BASE_URL` drives canonical links, Open Graph URLs, robots.txt, and sitemap.xml. `API_BASE_URL` drives documentation examples and API response links.

Only enable `PUBLIC_CANONICAL_REDIRECTS=true` when the proxy preserves the original `Host` header. The Go app redirects duplicate public HTML routes to `PUBLIC_BASE_URL`; it does not redirect API JSON routes.

`INDEX_ENABLED=true` opens the SQLite index during startup, runs migrations, and enables WAL mode. Startup fails loudly if the database cannot be opened. Make sure the service user can write to `/var/lib/reforgermods-api`; the packaged systemd unit already grants this path through `ReadWritePaths`.

Suggested production index settings:

```env
INDEX_POPULAR_PAGES=10
INDEX_RECENT_PAGES=5
INDEX_REFRESH_INTERVAL=30m
INDEX_DETAIL_REFRESH_CONCURRENCY=1
INDEX_LIST_REFRESH_CONCURRENCY=1
INDEX_HOT_LOAD_LIMIT=500
CACHE_LIST_FRESH_TTL=1h
CACHE_LIST_STALE_TTL=24h
CACHE_SEARCH_FRESH_TTL=10m
CACHE_SEARCH_STALE_TTL=2h
CACHE_MOD_DETAIL_FRESH_TTL=30m
CACHE_MOD_DETAIL_STALE_TTL=24h
CACHE_NOT_FOUND_TTL=15m
```

## Reverse Proxy

- Route `reforgermods.net` to the Go service for public HTML pages, `/robots.txt`, `/sitemap.xml`, and static assets.
- Route `api.reforgermods.net` to the same Go service for `/v1/*` and deprecated unversioned API paths if they are still required.
- Preserve `Host`, `X-Forwarded-For`, and `X-Forwarded-Proto`.
- Set `TRUSTED_PROXY_CIDRS` only to proxy IPs/CIDRs that can reach the origin.
- Keep `/internal/metrics` and `/internal/metrics/panel` private or disabled unless explicitly protected.

## Crawlability Validation

After deployment:

```bash
curl -I https://reforgermods.net/
curl -s https://reforgermods.net/ | rg '<title>|rel="canonical"|<h1'
curl -s https://reforgermods.net/robots.txt
curl -s https://reforgermods.net/sitemap.xml
curl -I https://api.reforgermods.net/v1/health
```

Expected results:

- Public pages return `200 OK`, a canonical URL on `https://reforgermods.net`, and indexable HTML.
- `/robots.txt` references `https://reforgermods.net/sitemap.xml`.
- `/sitemap.xml` lists every public indexable route.
- API JSON routes include `X-Robots-Tag: noindex, nofollow, noarchive`.

## Search Console

Manual steps after launch:

1. Add and verify the `https://reforgermods.net` property in Google Search Console.
2. Submit `https://reforgermods.net/sitemap.xml`.
3. Use URL Inspection for `/`, `/arma-reforger-mods/`, `/arma-reforger-mods-api/`, `/docs/`, and `/docs/methodology/`.
4. Confirm the selected canonical is the public URL, not `api.reforgermods.net`.
5. Inspect one API endpoint such as `https://api.reforgermods.net/v1/health` and confirm it is excluded because of `noindex`.
6. Recheck after DNS, Cloudflare, or reverse-proxy changes.

Search visibility and AI-answer inclusion are not guaranteed. This checklist only verifies that the site is crawlable, accurately described, and technically eligible for indexing.
