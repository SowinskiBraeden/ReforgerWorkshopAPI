(function () {
  function qs(name) {
    return new URLSearchParams(window.location.search).get(name) || '';
  }

  function status(text) {
    var el = document.querySelector('[data-billing-status]');
    if (el) el.textContent = text || '';
  }

  function request(path, options) {
    var opts = Object.assign({ credentials: 'same-origin', headers: { 'Content-Type': 'application/json' } }, options || {});
    return fetch(path, opts).then(function (res) {
      return res.json().then(function (body) {
        if (!res.ok) {
          var err = new Error(body && body.error ? body.error.message : 'Request failed');
          err.code = body && body.error ? body.error.code : '';
          throw err;
        }
        return body;
      });
    });
  }

  function checkoutButtons() {
    document.querySelectorAll('[data-checkout-plan]').forEach(function (button) {
      button.addEventListener('click', function () {
        button.disabled = true;
        status('Redirecting to secure Stripe Checkout...');
        request('/billing/checkout', { method: 'POST', body: JSON.stringify({ plan: button.getAttribute('data-checkout-plan') }) })
          .then(function (body) { window.location.href = body.url; })
          .catch(function (err) { button.disabled = false; status(err.message); });
      });
    });
  }

  function billingSuccess() {
    var successTarget = document.querySelector('[data-success-result]');
    if (!successTarget) return;
    var sessionID = qs('session_id');
    if (!sessionID) {
      document.querySelector('[data-success-message]').textContent = 'Missing Checkout session ID.';
      return;
    }
    request('/billing/session?session_id=' + encodeURIComponent(sessionID), { method: 'GET' }).then(function (body) {
      document.querySelector('[data-success-message]').textContent = body.message || 'Subscription verified.';
      var key = body.api_key
        ? '<div class="billing-reveal"><span class="billing-kicker">One-time API key</span><code class="billing-key">' + escapeHTML(body.api_key) + '</code><button class="billing-secondary" data-copy-key>Copy key</button></div>'
        : '<div class="billing-reveal"><span class="billing-kicker">Existing key</span><p>Key prefix: <code>' + escapeHTML(body.api_key_prefix || '') + '</code></p></div>';
      successTarget.innerHTML = '<div class="billing-account-strip"><div><span class="billing-kicker">Subscription</span><p>' + escapeHTML(displayPlan(body.plan)) + ' plan · ' + escapeHTML(body.status || '') + '</p></div><a class="billing-secondary" href="/account/api-keys/">Manage keys</a></div>' + key +
        '<p class="billing-note">This browser is now signed in. We also emailed a one-time sign-in link to <strong>' + escapeHTML(body.email || 'your email') + '</strong> so you can manage keys from any device.</p>';
      var copy = successTarget.querySelector('[data-copy-key]');
      if (copy) copy.addEventListener('click', function () { copyText(body.api_key, copy, 'Copied'); });
      successTarget.hidden = false;
    }).catch(function (err) {
      document.querySelector('[data-success-message]').textContent = err.message;
    });
  }

  /* ── Account pages (email magic-link sign-in) ─────────────────────── */

  function accountPage() {
    var page = document.querySelector('[data-account-api-keys], [data-account-billing]');
    if (!page) return;

    var loginResult = qs('login');
    if (loginResult === 'invalid') status('That sign-in link is invalid or has expired. Request a new one below.');
    if (loginResult === 'success') status('Signed in.');

    wireLoginForm();
    if (qs('checkout') === 'success' && qs('session_id')) {
      reconcileCheckoutSession();
      return;
    }
    showCheckoutSuccessNotice();
    request('/account/session', { method: 'GET' }).then(function (body) {
      if (body.authenticated) showDashboard(body);
      else showLogin();
    }).catch(function () { showLogin(); });
  }

  function showLogin() {
    toggle('[data-login-panel]', true);
    toggle('[data-dashboard]', false);
  }

  function showCheckoutSuccessNotice() {
    if (qs('checkout') !== 'success') return;
    var target = document.querySelector('[data-checkout-success]');
    if (!target) return;
    target.hidden = false;
    target.innerHTML = '<div class="billing-reveal"><span class="billing-kicker">Subscription active</span><p>Your subscription is active. Sign in with the email you used at checkout to create and manage API keys.</p></div>';
  }

  function reconcileCheckoutSession() {
    var target = document.querySelector('[data-checkout-success]');
    if (target) {
      target.hidden = false;
      target.innerHTML = '<div class="billing-reveal"><span class="billing-kicker">Subscription active</span><p>Verifying your Stripe Checkout session...</p></div>';
    }
    showLogin();
    request('/billing/session?session_id=' + encodeURIComponent(qs('session_id')), { method: 'GET' }).then(function (body) {
      var notice = checkoutSessionNotice(body);
      showDashboard({ email: body.email || '', plan: body.plan || '', subscription_status: body.status || '' });
      var dashboardTarget = document.querySelector('[data-checkout-dashboard-result]');
      if (dashboardTarget) {
        dashboardTarget.hidden = false;
        dashboardTarget.innerHTML = notice;
        var copy = dashboardTarget.querySelector('[data-copy-key]');
        if (copy) copy.addEventListener('click', function () { copyText(body.api_key, copy, 'Copied'); });
      }
    }).catch(function (err) {
      if (target) target.innerHTML = '<div class="billing-reveal"><span class="billing-kicker">Checkout needs attention</span><p>' + escapeHTML(err.message) + '</p></div>';
    });
  }

  function checkoutSessionNotice(body) {
    var key = body.api_key
      ? '<div class="billing-reveal"><span class="billing-kicker">One-time API key</span><code class="billing-key">' + escapeHTML(body.api_key) + '</code><button class="billing-secondary" data-copy-key>Copy key</button></div>'
      : '<div class="billing-reveal"><span class="billing-kicker">API key ready</span><p>Key prefix: <code>' + escapeHTML(body.api_key_prefix || '') + '</code></p></div>';
    return '<div class="billing-reveal"><span class="billing-kicker">Subscription active</span><p>' + escapeHTML(displayPlan(body.plan)) + ' plan · ' + escapeHTML(body.status || '') + '</p><p class="billing-note">Signed in as <strong>' + escapeHTML(body.email || 'your checkout email') + '</strong>.</p></div>' + key;
  }

  function showDashboard(session) {
    toggle('[data-login-panel]', false);
    toggle('[data-dashboard]', true);
    var summary = document.querySelector('[data-account-summary]');
    if (summary) {
      summary.innerHTML = escapeHTML(session.email) + ' ' + planBadge(session.plan) +
        ' <span class="billing-status-text">' + escapeHTML(session.subscription_status || 'unknown') + '</span>';
    }
    wireSignOut();
    wirePortal();
    wireCreateKey();
    if (document.querySelector('[data-account-api-keys]')) loadKeys();
  }

  function wireLoginForm() {
    var form = document.querySelector('[data-login-form]');
    if (!form) return;
    form.addEventListener('submit', function (event) {
      event.preventDefault();
      var input = document.getElementById('login-email');
      var email = input ? input.value.trim() : '';
      if (!email) return;
      var button = form.querySelector('button');
      if (button) button.disabled = true;
      status('Sending sign-in link...');
      request('/account/login', { method: 'POST', body: JSON.stringify({ email: email }) }).then(function (body) {
        status('');
        var panel = document.querySelector('[data-login-panel]');
        if (panel) panel.innerHTML = '<div class="billing-empty"><h2>Check your inbox</h2><p>' + escapeHTML(body.message || 'A sign-in link is on its way.') + '</p></div>';
      }).catch(function (err) {
        if (button) button.disabled = false;
        status(err.message);
      });
    });
  }

  function wireSignOut() {
    var button = document.querySelector('[data-sign-out]');
    if (!button || button.getAttribute('data-wired')) return;
    button.setAttribute('data-wired', 'true');
    button.addEventListener('click', function () {
      request('/account/logout', { method: 'POST' }).then(function () {
        window.location.href = window.location.pathname;
      });
    });
  }

  function wirePortal() {
    var button = document.querySelector('[data-open-portal]');
    if (!button || button.getAttribute('data-wired')) return;
    button.setAttribute('data-wired', 'true');
    button.addEventListener('click', function () {
      status('Opening Stripe Customer Portal...');
      request('/billing/portal', { method: 'POST' })
        .then(function (body) { window.location.href = body.url; })
        .catch(function (err) { status(err.message); });
    });
  }

  function wireCreateKey() {
    var form = document.querySelector('[data-create-key-form]');
    if (!form || form.getAttribute('data-wired')) return;
    form.setAttribute('data-wired', 'true');
    form.addEventListener('submit', function (event) {
      event.preventDefault();
      var nameInput = document.getElementById('key-name');
      var name = nameInput ? nameInput.value.trim() : '';
      status('Creating API key...');
      request('/account/api-keys', { method: 'POST', body: JSON.stringify({ name: name }) }).then(function (body) {
        status('');
        renderReveal(body);
        if (nameInput) nameInput.value = '';
        return loadKeys();
      }).catch(function (err) { status(err.message); });
    });
  }

  function loadKeys() {
    status('Loading keys...');
    return request('/account/api-keys', { method: 'GET' }).then(function (body) {
      status('');
      renderKeys(body);
      updateKeyAllowance(body);
      renderRateLimit(body.rate_limit);
    }).catch(function (err) {
      if (err.code === 'NOT_SIGNED_IN') showLogin();
      else status(err.message);
    });
  }

  function updateKeyAllowance(body) {
    var keys = body.api_keys || [];
    var limit = body.key_limit || 0;
    var atLimit = limit > 0 && keys.length >= limit;
    var count = document.querySelector('[data-key-count]');
    if (count && limit) {
      count.textContent = keys.length + ' of ' + limit + ' in use';
      count.classList.toggle('billing-key-count-max', atLimit);
    }
    var button = document.querySelector('[data-create-key-button]');
    if (button) button.disabled = atLimit;
    var nameInput = document.getElementById('key-name');
    if (nameInput) nameInput.disabled = atLimit;
    var note = document.querySelector('[data-key-limit-note]');
    if (note) note.hidden = !atLimit;
    var noteText = document.querySelector('[data-key-limit-text]');
    if (noteText && atLimit) {
      noteText.textContent = 'Your ' + displayPlan(body.plan) + ' plan includes ' + limit +
        ' active key' + (limit === 1 ? '' : 's') + '. Revoke a key you no longer use to create a new one.';
    }
  }

  function renderRateLimit(limit) {
    var target = document.querySelector('[data-rate-limit-summary]');
    if (!target || !limit) return;
    var values = [
      ['Requests', formatNumber(limit.limit_per_minute) + ' / minute'],
      ['Burst allowance', formatNumber(limit.burst)]
    ];
    target.innerHTML = values.map(function (item) {
      return '<div class="billing-rate-limit-item"><span>' + escapeHTML(item[0]) + '</span><strong>' + escapeHTML(item[1]) + '</strong></div>';
    }).join('');
    var hint = document.querySelector('[data-rate-limit-hint]');
    if (hint) hint.textContent = '';
  }

  function renderKeys(body) {
    var target = document.querySelector('[data-keys-result]');
    if (!target) return;
    var keys = body.api_keys || [];
    if (!keys.length) {
      target.innerHTML = '<div class="billing-empty"><h2>No active keys</h2><p>Create your first key below to start calling the API with your paid rate limit.</p></div>';
      return;
    }
    target.innerHTML = '<div class="billing-table" role="table" aria-label="Active API keys">' + keys.map(function (key) {
      return '<div class="billing-key-card" role="row">' +
        '<div><span class="billing-key-name">' + escapeHTML(key.name || 'Unnamed key') + '</span><code>' + escapeHTML(key.prefix || '') + '...' + escapeHTML(key.last_four || '') + '</code></div>' +
        planBadge(key.plan) +
        '<span class="billing-key-meta">Created ' + escapeHTML(formatDate(key.created_at)) + '<br>' + escapeHTML(key.last_used_at ? 'Last used ' + formatDate(key.last_used_at) : 'Never used') + '</span>' +
        '<button type="button" class="billing-secondary billing-revoke" data-revoke-key="' + escapeHTML(key.id) + '">Revoke</button>' +
        '</div>';
    }).join('') + '</div>';
    target.querySelectorAll('[data-revoke-key]').forEach(function (button) {
      button.addEventListener('click', function () {
        if (button.getAttribute('data-confirm') !== 'true') {
          button.setAttribute('data-confirm', 'true');
          button.textContent = 'Confirm revoke';
          button.classList.add('billing-danger');
          return;
        }
        status('Revoking key...');
        request('/account/api-keys/' + encodeURIComponent(button.getAttribute('data-revoke-key')), { method: 'DELETE' })
          .then(loadKeys)
          .catch(function (err) { status(err.message); });
      });
    });
  }

  function renderReveal(body) {
    var target = document.querySelector('[data-key-reveal]');
    if (!target) return;
    target.hidden = false;
    target.innerHTML = '<div class="billing-reveal"><span class="billing-kicker">New API key</span><p>' + escapeHTML(body.message || 'Store this API key now. It will only be shown once.') + '</p><code class="billing-key">' + escapeHTML(body.api_key || '') + '</code><button type="button" class="billing-secondary" data-copy-created-key>Copy key</button></div>';
    var copy = target.querySelector('[data-copy-created-key]');
    if (copy) copy.addEventListener('click', function () { copyText(body.api_key, copy, 'Copied'); });
  }

  function toggle(selector, show) {
    var el = document.querySelector(selector);
    if (el) el.hidden = !show;
  }

  function displayPlan(plan) {
    if (!plan) return 'Unknown';
    return String(plan).charAt(0).toUpperCase() + String(plan).slice(1);
  }

  function planBadge(plan) {
    var slug = String(plan || '').toLowerCase();
    var variant = (slug === 'developer' || slug === 'pro' || slug === 'free') ? ' billing-badge-' + slug : '';
    return '<span class="billing-badge' + variant + '">' + escapeHTML(displayPlan(plan)) + '</span>';
  }

  function formatDate(value) {
    if (!value) return 'unknown';
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return 'unknown';
    return date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
  }

  function formatNumber(value) {
    var n = Number(value || 0);
    if (!Number.isFinite(n)) return '0';
    return n.toLocaleString();
  }

  function copyText(value, button, label) {
    if (!value) return;
    navigator.clipboard.writeText(value).then(function () {
      button.textContent = label;
    }).catch(function () {
      button.textContent = 'Copy failed';
    });
  }

  function escapeHTML(value) {
    return String(value).replace(/[&<>"']/g, function (ch) { return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch]; });
  }

  checkoutButtons();
  billingSuccess();
  accountPage();
})();
