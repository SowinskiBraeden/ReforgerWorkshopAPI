<section class="landing-hero">
  <div class="landing-hero-copy">
    <div class="landing-kicker">Reforger Mods API</div>
    <h1>Arma Reforger mod metadata, ready for your app.</h1>
    <p class="landing-lede">A read-only API that serves normalized Arma Reforger Workshop data — mod lists, search results, and full detail pages.</p>
    <div class="landing-actions">
      <a href="?page=documentation/api" class="landing-primary-action"><i class="bi bi-terminal"></i> API Reference</a>
      <a href="/v1/health" class="landing-secondary-action">Health check</a>
    </div>
  </div>
  <div class="landing-panel" aria-label="API example">
    <div class="landing-panel-header">
      <div class="landing-panel-chrome"><span></span><span></span><span></span></div>
      <div class="landing-panel-label">Example request</div>
    </div>
    <code>GET /v1/mods?search=radio&amp;sort=newest</code>
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
  <a href="/v1/mods" class="landing-link-card">
    <i class="bi bi-box-arrow-up-right"></i>
    <span>Try the API</span>
    <small>Open the first cached Workshop list response in your browser.</small>
  </a>
</div>
