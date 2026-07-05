<section class="landing-hero">
  <div class="landing-hero-copy">
    <div class="landing-kicker">Reforger Mods API</div>
    <h1>Arma Reforger mod metadata, ready for your app.</h1>
    <p class="landing-lede">Search Arma Reforger Workshop mods and fetch normalized metadata through a public read-only API for mod lists, detail pages, dependencies, scenarios, ratings, downloads, and Workshop links.</p>
    <div class="landing-actions">
      <a href="?page=documentation/api" class="landing-primary-action"><i class="bi bi-terminal"></i> API Reference</a>
      <a href="https://api.reforgermods.net/v1/health" class="landing-secondary-action">Health check</a>
    </div>
  </div>
  <div class="landing-panel" aria-label="API example">
    <div class="landing-panel-header">
      <div class="landing-panel-chrome"><span></span><span></span><span></span></div>
      <div class="landing-panel-label">Example request</div>
    </div>
    <code>GET https://api.reforgermods.net/v1/mods?search=radio&amp;sort=newest</code>
    <div class="landing-panel-meta">
      <span><i class="bi bi-check2-circle"></i> Normalized JSON responses</span>
      <span><i class="bi bi-clock"></i> Stale-while-revalidate cache</span>
      <span><i class="bi bi-shield-check"></i> Rate limited &amp; safe</span>
    </div>
  </div>
</section>

<div class="landing-metrics" aria-label="API defaults">
  <div>
    <span class="landing-status-label">Version</span>
    <strong>/v1</strong>
  </div>
  <div>
    <span class="landing-status-label">Public limit</span>
    <strong>60 / min</strong>
  </div>
  <div>
    <span class="landing-status-label">Mod cache</span>
    <strong>1 h fresh</strong>
  </div>
</div>

<p class="landing-note">Cached responses may be temporarily stale. Workshop layout, fields, and availability are controlled by Bohemia Interactive and can change upstream without notice.</p>

## Arma Reforger Workshop Data for Developers

Reforger Mods API helps apps, dashboards, launchers, server tools, and community sites work with Arma Reforger mods without scraping Workshop pages directly. The API exposes searchable Workshop mod previews and full mod detail responses as predictable JSON.

Use it when you need an Arma Reforger API for mod discovery, Workshop metadata, dependency lookups, scenario data, or links back to the official Bohemia Interactive Workshop pages.

## Common Arma Reforger Mod API Use Cases

- Find Arma Reforger mods by Workshop search text and sort order.
- Read mod names, authors, images, ratings, sizes, subscribers, downloads, versions, and game versions.
- Resolve mod dependencies and scenario metadata for server or community tools.
- Link each API response back to the official Arma Reforger Workshop mod page.

<div class="landing-grid">
  <a href="?page=documentation/api" class="landing-link-card">
    <i class="bi bi-braces"></i>
    <span>API Reference</span>
    <small>Routes, examples, cache headers, error codes, and rate limits.</small>
  </a>
  <a href="?page=documentation/mods" class="landing-link-card">
    <i class="bi bi-diagram-3"></i>
    <span>Mod Structures</span>
    <small>Preview, detail, dependency, and scenario response fields.</small>
  </a>
  <a href="https://api.reforgermods.net/v1/mods" class="landing-link-card">
    <i class="bi bi-box-arrow-up-right"></i>
    <span>Try the API</span>
    <small>Open the first cached Workshop list response in your browser.</small>
  </a>
</div>
