package handlers

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/models"
	"github.com/gorilla/mux"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/config"
)

// App stores the router and db connection so it can be reused
type App struct {
	Router     *mux.Router
	Config     config.Config
	Cache      *api.ResponseCache
	Middleware *api.MiddlewareChain
	Metrics    *api.Metrics
}

// New creates a new mux router and all the routes
func (a *App) New() *mux.Router {

	router := mux.NewRouter()

	// apiCreate := r.PathPrefix("/api").Subrouter()

	// Serve static files
	router.PathPrefix("/static/").Handler(http.StripPrefix("/static/", http.FileServer(http.Dir("./static/"))))

	router.HandleFunc("/ads.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/ads.txt")
	})
	router.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/robots.txt")
	})
	router.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/sitemap.xml")
	})

	// Serve index page on all unhandled routes
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./static/index.html")
	})

	a.Metrics = api.NewMetrics()
	a.Cache = api.NewResponseCache(a.Config, a.Metrics)
	a.Middleware = api.NewMiddleware(a.Config, a.Metrics)

	// API Routes. Unversioned routes are retained as deprecated aliases.
	v1 := router.PathPrefix("/v1").Subrouter()
	a.registerAPIRoutes(v1, false)
	a.registerAPIRoutes(router, true)
	router.HandleFunc("/internal/metrics", a.internalMetricsHandler).Methods("GET")
	router.HandleFunc("/internal/metrics/panel", a.internalMetricsPanelHandler).Methods("GET")
	v1.HandleFunc("/internal/metrics", a.internalMetricsHandler).Methods("GET")
	v1.HandleFunc("/internal/metrics/panel", a.internalMetricsPanelHandler).Methods("GET")

	return router
}

func (a *App) registerAPIRoutes(router *mux.Router, deprecated bool) {
	wrap := func(handler http.HandlerFunc) http.Handler {
		var h http.Handler = handler
		if deprecated {
			h = deprecatedRoute(h)
		}
		return a.Middleware.Wrap(h)
	}
	router.Handle("/health", wrap(healthCheckHandler)).Methods("GET")
	router.Handle("/mod/{id}", wrap(a.ModByIDHandler)).Methods("GET")
	router.Handle("/mods", wrap(a.ModsHandler)).Methods("GET")
	router.Handle("/mods/{page}", wrap(a.ModsByPageHandler)).Methods("GET")
	router.Handle("/search", wrap(a.SearchHandler)).Methods("GET")
}

func (a *App) Initialize() {
	// initialize api router
	a.Router = a.New()
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	b, _ := json.Marshal(models.HealthCheckResponse{
		Status: "success",
		Data: models.HealthCheckData{
			Code:  http.StatusOK,
			Alive: true,
		},
	})
	_, _ = io.Writer.Write(w, b)
}

func (a *App) internalMetricsHandler(w http.ResponseWriter, r *http.Request) {
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !internalMetricsAllowed(r, a.Config.InternalMetricsToken) {
		writeMetricsUnauthorized(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if a.Metrics == nil {
		a.Metrics = api.NewMetrics()
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(a.Metrics.Snapshot(a.Cache))
}

func (a *App) internalMetricsPanelHandler(w http.ResponseWriter, r *http.Request) {
	if !a.Config.InternalMetricsEnabled {
		config.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "Not found.")
		return
	}
	if !internalMetricsAllowed(r, a.Config.InternalMetricsToken) {
		writeMetricsUnauthorized(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, internalMetricsPanelHTML)
}

func internalMetricsAllowed(r *http.Request, token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return constantTimeEqual(metricsTokenFromRequest(r), token)
}

func metricsTokenFromRequest(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-Internal-Metrics-Token")); token != "" {
		return token
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if strings.HasPrefix(strings.ToLower(auth), "basic ") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
		if err == nil {
			_, password, ok := strings.Cut(string(decoded), ":")
			if ok {
				return password
			}
		}
	}
	return ""
}

func writeMetricsUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Internal Metrics", charset="UTF-8"`)
	config.WriteError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Metrics token is required.")
}

func constantTimeEqual(a string, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

const internalMetricsPanelHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Internal Metrics | Reforger Mods API</title>
  <style>
    :root { color-scheme: dark; --bg: #0b1118; --panel: #111a24; --panel-2: #162231; --text: #e8eef5; --muted: #8fa2b7; --line: #253446; --accent: #26c29a; --warn: #f4b860; --bad: #ff6b6b; }
    * { box-sizing: border-box; }
    body { margin: 0; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: var(--bg); color: var(--text); }
    header { position: sticky; top: 0; z-index: 1; background: rgba(11, 17, 24, .92); border-bottom: 1px solid var(--line); backdrop-filter: blur(10px); }
    .wrap { max-width: 1220px; margin: 0 auto; padding: 20px; }
    .top { display: flex; gap: 16px; align-items: center; justify-content: space-between; }
    h1 { margin: 0; font-size: 22px; font-weight: 700; }
    .sub { margin-top: 4px; color: var(--muted); font-size: 13px; }
    .controls { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
    input, select, button { height: 36px; border-radius: 6px; border: 1px solid var(--line); background: var(--panel); color: var(--text); padding: 0 10px; }
    input { width: 230px; }
    button { cursor: pointer; background: var(--accent); border-color: var(--accent); color: #06120f; font-weight: 700; }
    main.wrap { display: grid; gap: 18px; }
    .grid { display: grid; gap: 14px; grid-template-columns: repeat(4, minmax(0, 1fr)); }
    .card { background: var(--panel); border: 1px solid var(--line); border-radius: 8px; padding: 16px; min-width: 0; }
    .label { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
    .value { margin-top: 8px; font-size: 28px; font-weight: 750; line-height: 1; }
    .small { color: var(--muted); font-size: 13px; margin-top: 8px; }
    .section-title { display: flex; justify-content: space-between; gap: 10px; align-items: baseline; margin-bottom: 12px; }
    h2 { margin: 0; font-size: 16px; }
    .bars { display: grid; gap: 8px; }
    .bar-row { display: grid; grid-template-columns: 92px 1fr 58px; gap: 10px; align-items: center; color: var(--muted); font-size: 12px; }
    .track { height: 10px; overflow: hidden; border-radius: 99px; background: var(--panel-2); }
    .fill { height: 100%; border-radius: inherit; background: var(--accent); min-width: 1px; }
    table { width: 100%; border-collapse: collapse; font-size: 13px; }
    th, td { padding: 9px 8px; border-bottom: 1px solid var(--line); text-align: left; vertical-align: top; }
    th { color: var(--muted); font-weight: 600; }
    code { color: var(--accent); word-break: break-all; }
    .split { display: grid; grid-template-columns: 1.1fr .9fr; gap: 14px; }
    .status { color: var(--muted); font-size: 13px; }
    .error { color: var(--bad); }
    @media (max-width: 920px) { .grid, .split { grid-template-columns: 1fr; } .top { align-items: flex-start; flex-direction: column; } }
  </style>
</head>
<body>
  <header>
    <div class="wrap top">
      <div>
        <h1>Internal Metrics</h1>
        <div class="sub" id="meta">Loading metrics...</div>
      </div>
      <div class="controls">
        <input id="token" type="password" autocomplete="off" placeholder="Bearer token">
        <select id="window">
          <option value="day">Day</option>
          <option value="week">Week</option>
          <option value="month">Month</option>
          <option value="year">Year</option>
        </select>
        <button id="refresh">Refresh</button>
      </div>
    </div>
  </header>
  <main class="wrap">
    <section class="grid">
      <div class="card"><div class="label">Requests Today</div><div class="value" id="requestsToday">0</div><div class="small" id="requestsTotal">0 total</div></div>
      <div class="card"><div class="label">Average Response</div><div class="value" id="averageMs">0 ms</div><div class="small" id="rangeMs">low 0 ms / high 0 ms</div></div>
      <div class="card"><div class="label">Cache</div><div class="value" id="cacheRate">0%</div><div class="small" id="cacheCounts">0 hits / 0 misses / 0 stale</div></div>
      <div class="card"><div class="label">Scrapes</div><div class="value" id="scrapes">0</div><div class="small" id="scrapeErrors">0 errors</div></div>
    </section>
    <section class="card">
      <div class="section-title"><h2>Retention</h2><span class="status" id="retentionLabel"></span></div>
      <div class="bars" id="bars"></div>
    </section>
    <section class="split">
      <div class="card">
        <div class="section-title"><h2>Latest Cache Entries</h2><span class="status" id="entryCount"></span></div>
        <table><thead><tr><th>Key</th><th>Status</th><th>Age</th><th>Fresh</th><th>Stale</th></tr></thead><tbody id="cacheEntries"></tbody></table>
      </div>
      <div class="card">
        <div class="section-title"><h2>Latest Scrapes</h2><span class="status" id="scrapeCount"></span></div>
        <table><thead><tr><th>Time</th><th>Key</th><th>Duration</th><th>Status</th></tr></thead><tbody id="latestScrapes"></tbody></table>
      </div>
    </section>
    <div class="status" id="status"></div>
  </main>
  <script>
    const token = document.getElementById('token');
    const savedToken = sessionStorage.getItem('metricsToken') || '';
    token.value = savedToken;
    token.addEventListener('input', () => sessionStorage.setItem('metricsToken', token.value));
    document.getElementById('refresh').addEventListener('click', load);
    document.getElementById('window').addEventListener('change', () => renderRetention(window.latestMetrics));
    const fmt = new Intl.NumberFormat();
    const timeFmt = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', month: 'short', day: 'numeric' });
    async function load() {
      const headers = {};
      if (token.value.trim()) headers.Authorization = 'Bearer ' + token.value.trim();
      const res = await fetch('/internal/metrics', { headers, cache: 'no-store' });
      if (!res.ok) {
        document.getElementById('status').innerHTML = '<span class="error">Metrics request failed: ' + res.status + '</span>';
        return;
      }
      window.latestMetrics = await res.json();
      render(window.latestMetrics);
    }
    function render(data) {
      document.getElementById('meta').textContent = 'Generated ' + new Date(data.generatedAt).toLocaleString() + ' · uptime ' + fmt.format(data.uptimeSeconds) + 's';
      document.getElementById('requestsToday').textContent = fmt.format(data.requests.today);
      document.getElementById('requestsTotal').textContent = fmt.format(data.requests.total) + ' total · ' + fmt.format(data.requests.thisWeek) + ' week · ' + fmt.format(data.requests.thisMonth) + ' month';
      document.getElementById('averageMs').textContent = Math.round(data.responseTime.averageMs) + ' ms';
      document.getElementById('rangeMs').textContent = 'low ' + data.responseTime.lowMs + ' ms / high ' + data.responseTime.highMs + ' ms';
      const cacheTotal = Math.max(1, data.cache.hits + data.cache.misses + data.cache.stales);
      document.getElementById('cacheRate').textContent = Math.round((data.cache.hits / cacheTotal) * 100) + '%';
      document.getElementById('cacheCounts').textContent = fmt.format(data.cache.hits) + ' hits / ' + fmt.format(data.cache.misses) + ' misses / ' + fmt.format(data.cache.stales) + ' stale';
      document.getElementById('scrapes').textContent = fmt.format(data.scrapes.total);
      document.getElementById('scrapeErrors').textContent = fmt.format(data.scrapes.errors) + ' errors';
      renderRetention(data);
      renderCacheEntries(data.cache.latestEntries || []);
      renderScrapes(data.scrapes.latestEvents || []);
      document.getElementById('status').textContent = '';
    }
    function renderRetention(data) {
      if (!data) return;
      const selected = document.getElementById('window').value;
      const view = data.retention[selected];
      document.getElementById('retentionLabel').textContent = view.window + ' · ' + view.bucketSize + ' buckets';
      const max = Math.max(1, ...view.buckets.map(b => b.requests));
      document.getElementById('bars').innerHTML = view.buckets.map(b => {
        const width = Math.max(2, Math.round((b.requests / max) * 100));
        return '<div class="bar-row"><span>' + labelFor(selected, b.start) + '</span><div class="track"><div class="fill" style="width:' + width + '%"></div></div><strong>' + fmt.format(b.requests) + '</strong></div>';
      }).join('');
    }
    function renderCacheEntries(entries) {
      document.getElementById('entryCount').textContent = fmt.format(entries.length) + ' shown';
      document.getElementById('cacheEntries').innerHTML = entries.map(e => '<tr><td><code>' + escapeHtml(e.key) + '</code></td><td>' + e.currentStatus + '</td><td>' + e.ageSeconds + 's</td><td>' + e.freshSeconds + 's</td><td>' + e.staleSeconds + 's</td></tr>').join('') || '<tr><td colspan="5">No cache entries yet.</td></tr>';
    }
    function renderScrapes(events) {
      document.getElementById('scrapeCount').textContent = fmt.format(events.length) + ' shown';
      document.getElementById('latestScrapes').innerHTML = events.map(e => '<tr><td>' + timeFmt.format(new Date(e.at)) + '</td><td><code>' + escapeHtml(e.key || '') + '</code></td><td>' + (e.durationMs || 0) + ' ms</td><td>' + (e.error ? '<span class="error">' + escapeHtml(e.error) + '</span>' : (e.statusCode || 'ok')) + '</td></tr>').join('') || '<tr><td colspan="4">No scrapes yet.</td></tr>';
    }
    function labelFor(view, value) {
      const date = new Date(value);
      if (view === 'day') return date.getHours().toString().padStart(2, '0') + ':00';
      if (view === 'year') return date.toLocaleDateString(undefined, { month: 'short', year: '2-digit' });
      return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
    }
    function escapeHtml(value) {
      return String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#039;' }[c]));
    }
    load();
    setInterval(load, 30000);
  </script>
</body>
</html>`

func deprecatedRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Deprecation", "true")
		w.Header().Set("Link", `</v1`+r.URL.Path+`>; rel="successor-version"`)
		next.ServeHTTP(w, r)
	})
}
