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

- `/mods/` - searchable Workshop mod browser with mod detail pages
- `/config-generator/` - form-based server `config.json` builder with live preview and export
- `/config-validator/` - local, in-browser `config.json` validation with optional mod ID checks
- `/mod-manager/` - editor for the `game.mods` array with name resolution and dependency suggestions
- `/guides/` - server config and API integration guides

Config editing happens client-side; configs are never uploaded.

## Search Console Deployment Checklist

1. Deploy SEO changes.
2. Verify canonical URLs for the homepage, `/mods/`, `/config-generator/`, `/config-validator/`, `/mod-manager/`, `/api/`, `/pricing/`, and guide pages.
3. Open `/sitemap.xml` and confirm it includes public tools, guides, API docs, pricing, and changelog pages.
4. Confirm `/robots.txt` allows public tools and guides while excluding account, billing, API JSON, and internal paths.
5. Test structured data for core tool pages and guides.
6. Submit the sitemap in Google Search Console.
7. Request indexing for core tool pages.
8. Monitor impressions, clicks, CTR, and average position.

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
CACHE_LIST_FRESH_TTL=1h
CACHE_LIST_STALE_TTL=24h
CACHE_SEARCH_FRESH_TTL=10m
CACHE_SEARCH_STALE_TTL=2h
CACHE_MOD_DETAIL_FRESH_TTL=30m
CACHE_MOD_DETAIL_STALE_TTL=24h
CACHE_NOT_FOUND_TTL=15m
CACHE_MAX_ENTRIES=1000
CACHE_REFRESH_CONCURRENCY=8
CACHE_REFRESH_QUEUE_SIZE=64
UPSTREAM_TIMEOUT=15s
UPSTREAM_RETRIES=2
UPSTREAM_CONCURRENCY=4
```

Persistent local indexing can be enabled for production or long-running local instances:

```text
INDEX_ENABLED=true
INDEX_DB_PATH=/var/lib/reforgermods-api/reforgermods-index.db
INDEX_REFRESH_ENABLED=true
INDEX_POPULAR_PAGES=10
INDEX_RECENT_PAGES=5
INDEX_REFRESH_INTERVAL=30m
INDEX_DETAIL_REFRESH_CONCURRENCY=1
INDEX_LIST_REFRESH_CONCURRENCY=1
INDEX_HOT_LOAD_LIMIT=500
```

When enabled, SQLite is opened in WAL mode and used as a warm cache for API response bodies, list pages, mod metadata, mod details, and local mod search. The in-memory cache remains the hot layer. Unknown resources still return the existing `202 Accepted` refresh-job response.

Stripe subscription billing for paid API keys is optional:

```text
BILLING_ENABLED=true
BILLING_DB_PATH=/var/lib/reforgermods-api/reforgermods-billing.db
STRIPE_SECRET_KEY=${STRIPE_SECRET_KEY}
STRIPE_PUBLISHABLE_KEY=${STRIPE_PUBLISHABLE_KEY}
STRIPE_WEBHOOK_SECRET=whsec_...
STRIPE_DEVELOPER_PRICE_ID=price_...
STRIPE_PRO_PRICE_ID=price_...
BILLING_SUCCESS_URL=https://reforgermods.net/account/api-keys/?checkout=success&session_id={CHECKOUT_SESSION_ID}
BILLING_CANCEL_URL=https://reforgermods.net/pricing
BILLING_PORTAL_RETURN_URL=https://reforgermods.net/account/billing
API_KEY_HASH_SECRET=replace-with-random-secret
ACCOUNT_SESSION_SECRET=replace-with-random-secret
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=apikey-or-username
SMTP_PASSWORD=${SMTP_PASSWORD}
SMTP_FROM=Reforger Mods API <no-reply@reforgermods.net>
RATE_LIMIT_FREE_PER_MINUTE=60
RATE_LIMIT_DEVELOPER_PER_MINUTE=300
RATE_LIMIT_PRO_PER_MINUTE=1200
RATE_LIMIT_INTERNAL_PER_MINUTE=5000
DEVELOPER_MAX_ACTIVE_KEYS=2
PRO_MAX_ACTIVE_KEYS=10
```

Paid rate limits are enforced per account, shared across all of an account's keys, so extra keys never multiply throughput. Active key counts are capped per plan (`DEVELOPER_MAX_ACTIVE_KEYS`/`PRO_MAX_ACTIVE_KEYS`); revoking a key frees its slot immediately.

Get `STRIPE_SECRET_KEY`, `STRIPE_PUBLISHABLE_KEY`, price IDs, and webhook signing secrets from the Stripe Dashboard. Put Stripe secrets, `API_KEY_HASH_SECRET`, and `ACCOUNT_SESSION_SECRET` in the systemd environment file, not in the repository. Checkout Sessions are created by `POST /billing/checkout` with server-side mapped Stripe price IDs and `mode=subscription`. Configure Stripe webhooks to send subscription lifecycle events to `POST /stripe/webhook`.

Key management uses passwordless email sign-in. The email used at Stripe Checkout is the account: subscribers request a one-time sign-in link (`POST /account/login`), the link (`GET /account/verify`) sets a signed session cookie, and the cookie authenticates key management (`/account/api-keys`) and the Customer Portal (`/billing/portal`). Sign-in links are delivered over SMTP; without SMTP configured they are logged instead, which is only suitable for local development. The webhook also emails a sign-in link after checkout so key access never depends on the browser that paid.

The internal admin panel at `/internal/metrics/panel` uses username/password login:

```text
INTERNAL_METRICS_ENABLED=true
INTERNAL_ADMIN_USERNAME=admin
INTERNAL_ADMIN_PASSWORD=replace-with-random-password
INTERNAL_ADMIN_SESSION_SECRET=replace-with-random-secret
```

The panel includes aggregate metrics, recent redacted request logs, API client summaries, and subscriber/key diagnostics.

Manual Stripe sandbox checklist:

1. Start the app with `sk_test`, `whsec`, Developer price, and Pro price values.
2. Open `/pricing`.
3. Click Developer.
4. Complete Stripe Checkout using test card `4242 4242 4242 4242`.
5. Confirm redirect to `/account/api-keys/?checkout=success&session_id=cs_...`.
6. Request the sign-in link for the checkout email.
7. Create an API key after signing in.
8. Call the API with `X-API-Key`.
9. Confirm `X-API-Plan` is `developer`.
10. Open Customer Portal from `/account/billing`.
11. Cancel the subscription.
12. Confirm the webhook downgrades the account and revokes paid API access.

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
CGO_ENABLED=1 go build -o reforger-workshop-api .
./reforger-workshop-api
```

SQLite support uses `github.com/mattn/go-sqlite3`, so production binaries must be built with CGO enabled and a C compiler installed. On Debian/Ubuntu, install `build-essential` before building.

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

The API uses an in-memory cache, an optional SQLite-backed persistent index, and a bounded background refresh queue. Public API requests do not wait indefinitely for slow Workshop scrapes.

- Fresh cache entries return `200 OK` with `X-Cache: HIT`.
- Stale but usable entries return `200 OK` with `X-Cache: STALE` and queue a background refresh.
- When the memory cache is cold, fresh or stale persistent SQLite entries are promoted back into memory and served with the same public response schema.
- Cold cache requests return `202 Accepted` with a refresh job location.
- Saturated refresh queues return `503 Service Unavailable` with `Retry-After`.

Every API response includes an `X-Request-Id` header. Error responses include the same value in the JSON body.

Logs are structured JSON and are written to stdout and daily files by default. Old daily logs can be cleaned with:

```bash
LOG_RETENTION_DAYS=14 ./scripts/clean-logs.sh
```
