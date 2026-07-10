/*
 * Shared client library for the Reforger Mods web tools.
 *
 * The site and the JSON API are served by the same Go service, so tools call
 * the API same-origin under /v1. Handles the cold-cache 202 Accepted flow
 * (wait Retry-After, retry the same URL, bounded attempts), keeps a small
 * sessionStorage response cache, and owns the shared "working config" that
 * the mod browser, mod manager, and config generator all edit.
 */
window.RM = (function () {
  'use strict';

  var MOD_ID_RE = /^[0-9A-F]{16}$/i;
  var STORE_KEY = 'reforgermods.workingConfig.v1';
  var CACHE_PREFIX = 'rm.cache.';
  var CACHE_TTL_MS = 5 * 60 * 1000;
  var CACHE_MAX_ENTRIES = 40;
  var MAX_202_RETRIES = 4;
  var CLIENT_HEADER = 'reforgermods.net-tools/1.0';

  function sleep(ms) {
    return new Promise(function (resolve) { setTimeout(resolve, ms); });
  }

  function ApiError(message, opts) {
    var err = new Error(message);
    err.name = 'ApiError';
    err.status = (opts && opts.status) || 0;
    err.code = (opts && opts.code) || '';
    err.stillRefreshing = !!(opts && opts.stillRefreshing);
    return err;
  }

  /*
   * fetchJSON('/v1/mods?search=x', { onWait: fn }) -> { data, cache, status }
   * onWait(attempt, seconds) fires before each 202 retry so pages can show a
   * "fetching fresh Workshop data" state. 202 is never treated as an error
   * until retries are exhausted.
   */
  async function fetchJSON(path, opts) {
    opts = opts || {};
    var attempts = opts.maxRetries || MAX_202_RETRIES;
    var res = null;
    for (var attempt = 1; attempt <= attempts; attempt++) {
      res = await fetch(path, {
        headers: { 'Accept': 'application/json', 'X-API-Client': CLIENT_HEADER },
        signal: opts.signal
      });
      if (res.status === 202) {
        var wait = parseInt(res.headers.get('Retry-After'), 10);
        if (!wait || wait < 1) wait = 2;
        if (wait > 10) wait = 10;
        if (attempt === attempts) break;
        if (opts.onWait) opts.onWait(attempt, wait);
        await sleep(wait * 1000);
        continue;
      }
      var body = null;
      try { body = await res.json(); } catch (e) { body = null; }
      if (!res.ok) {
        var apiErr = body && body.error ? body.error : {};
        throw ApiError(apiErr.message || ('API error ' + res.status), {
          status: res.status,
          code: apiErr.code || ''
        });
      }
      return {
        data: body,
        status: res.status,
        cache: res.headers.get('X-Cache') || ''
      };
    }
    throw ApiError('Workshop data is still being fetched. Try again shortly.', {
      status: 202,
      code: 'STILL_REFRESHING',
      stillRefreshing: true
    });
  }

  /* sessionStorage-backed cache for list/search responses. */
  function cacheGet(path) {
    try {
      var raw = sessionStorage.getItem(CACHE_PREFIX + path);
      if (!raw) return null;
      var entry = JSON.parse(raw);
      if (!entry || Date.now() - entry.t > CACHE_TTL_MS) {
        sessionStorage.removeItem(CACHE_PREFIX + path);
        return null;
      }
      return entry.v;
    } catch (e) {
      return null;
    }
  }

  function cacheSet(path, value) {
    try {
      var keys = [];
      for (var i = 0; i < sessionStorage.length; i++) {
        var k = sessionStorage.key(i);
        if (k && k.indexOf(CACHE_PREFIX) === 0) keys.push(k);
      }
      if (keys.length >= CACHE_MAX_ENTRIES) {
        keys.sort(function (a, b) {
          var ta = 0; var tb = 0;
          try { ta = JSON.parse(sessionStorage.getItem(a)).t || 0; } catch (e) {}
          try { tb = JSON.parse(sessionStorage.getItem(b)).t || 0; } catch (e) {}
          return ta - tb;
        });
        sessionStorage.removeItem(keys[0]);
      }
      sessionStorage.setItem(CACHE_PREFIX + path, JSON.stringify({ t: Date.now(), v: value }));
    } catch (e) { /* storage full or unavailable; caching is best-effort */ }
  }

  async function fetchJSONCached(path, opts) {
    var hit = cacheGet(path);
    if (hit) return hit;
    var result = await fetchJSON(path, opts);
    cacheSet(path, result);
    return result;
  }

  /* Resolve one mod's details, cached. Returns the mod object or throws. */
  async function fetchMod(modId, opts) {
    var path = '/v1/mod/' + encodeURIComponent(modId);
    var result = await fetchJSONCached(path, opts);
    var mod = result.data && (result.data.mod || result.data.data);
    if (!mod || !mod.name) {
      throw ApiError('Mod was not found.', { status: 404, code: 'NOT_FOUND' });
    }
    return mod;
  }

  /* ---- Working config (shared across tools via localStorage) ---- */

  function defaultConfig() {
    // Baseline mirrors commonly documented server defaults; see
    // /guides/arma-reforger-config-json/ and the official server docs.
    return {
      bindAddress: '0.0.0.0',
      bindPort: 2001,
      publicAddress: '',
      publicPort: 2001,
      a2s: { address: '0.0.0.0', port: 17777 },
      game: {
        name: '',
        password: '',
        passwordAdmin: '',
        admins: [],
        scenarioId: '{ECC61978EDCC2B5A}Missions/23_Campaign.conf',
        maxPlayers: 32,
        visible: true,
        crossPlatform: true,
        supportedPlatforms: ['PLATFORM_PC'],
        gameProperties: {
          enableAI: true,
          serverMaxViewDistance: 1600,
          serverMinGrassDistance: 0,
          networkViewDistance: 1500,
          disableThirdPerson: false,
          fastValidation: true,
          battlEye: true,
          VONDisableUI: false,
          VONDisableDirectSpeechUI: false,
          VONCanTransmitCrossFaction: false,
          missionHeader: {}
        },
        mods: []
      },
      operating: {
        enableAI: true,
        lobbyPlayerSynchronise: true,
        playerSaveTime: 120,
        aiLimit: 120,
        joinQueue: {
          maxSize: 50
        }
      }
    };
  }

  function loadConfig() {
    try {
      var raw = localStorage.getItem(STORE_KEY);
      if (!raw) return null;
      var cfg = JSON.parse(raw);
      return (cfg && typeof cfg === 'object' && !Array.isArray(cfg)) ? cfg : null;
    } catch (e) {
      return null;
    }
  }

  function saveConfig(cfg) {
    try {
      localStorage.setItem(STORE_KEY, JSON.stringify(cfg));
    } catch (e) { /* private mode / quota; edits still work in-page */ }
    window.dispatchEvent(new CustomEvent('rm-config-changed'));
  }

  function ensureConfig() {
    return loadConfig() || defaultConfig();
  }

  /* Returns the game.mods array, creating the path if needed. */
  function configMods(cfg) {
    if (!cfg.game || typeof cfg.game !== 'object' || Array.isArray(cfg.game)) {
      cfg.game = { mods: [] };
    }
    if (!Array.isArray(cfg.game.mods)) cfg.game.mods = [];
    return cfg.game.mods;
  }

  /* Adds a mod entry; refuses duplicates. Returns {added, duplicate, count}. */
  function addModToConfig(entry) {
    var cfg = ensureConfig();
    var mods = configMods(cfg);
    var id = String(entry.modId || '').trim();
    var exists = mods.some(function (m) {
      return m && typeof m === 'object' && String(m.modId || '').toUpperCase() === id.toUpperCase();
    });
    if (exists) return { added: false, duplicate: true, count: mods.length };
    var item = { modId: id };
    if (entry.name) item.name = String(entry.name);
    if (entry.version) item.version = String(entry.version);
    mods.push(item);
    saveConfig(cfg);
    return { added: true, duplicate: false, count: mods.length };
  }

  function removeModAt(index) {
    var cfg = ensureConfig();
    var mods = configMods(cfg);
    if (index >= 0 && index < mods.length) {
      mods.splice(index, 1);
      saveConfig(cfg);
    }
  }

  function moveMod(index, delta) {
    var cfg = ensureConfig();
    var mods = configMods(cfg);
    var target = index + delta;
    if (index < 0 || index >= mods.length || target < 0 || target >= mods.length) return;
    var item = mods.splice(index, 1)[0];
    mods.splice(target, 0, item);
    saveConfig(cfg);
  }

  function duplicateModIds(mods) {
    var seen = {};
    var dupes = {};
    (mods || []).forEach(function (m) {
      if (!m || typeof m !== 'object') return;
      var id = String(m.modId || '').toUpperCase();
      if (!id) return;
      if (seen[id]) dupes[id] = true;
      seen[id] = true;
    });
    return dupes;
  }

  /* ---- Small DOM/format helpers ---- */

  function esc(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#039;');
  }

  function debounce(fn, ms) {
    var timer = null;
    return function () {
      var args = arguments;
      var self = this;
      clearTimeout(timer);
      timer = setTimeout(function () { fn.apply(self, args); }, ms);
    };
  }

  function copyText(text, btn) {
    var done = function (ok) {
      if (!btn) return;
      var original = btn.innerHTML;
      btn.innerHTML = ok ? '<i class="bi bi-check-lg"></i> Copied' : 'Copy failed';
      btn.disabled = true;
      setTimeout(function () { btn.innerHTML = original; btn.disabled = false; }, 1400);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { done(true); }, function () { done(false); });
    } else {
      var area = document.createElement('textarea');
      area.value = text;
      document.body.appendChild(area);
      area.select();
      var ok = false;
      try { ok = document.execCommand('copy'); } catch (e) {}
      document.body.removeChild(area);
      done(ok);
    }
  }

  function downloadFile(filename, text) {
    var blob = new Blob([text], { type: 'application/json' });
    var url = URL.createObjectURL(blob);
    var link = document.createElement('a');
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
  }

  function formatJSON(value) {
    return JSON.stringify(value, null, 2) + '\n';
  }

  function modSnippet(mod) {
    var entry = { modId: mod.modId || mod.ID || mod.id || '' };
    if (mod.name) entry.name = mod.name;
    if (mod.version) entry.version = mod.version;
    return JSON.stringify(entry, null, 2);
  }

  function isModId(value) {
    return MOD_ID_RE.test(String(value || '').trim());
  }

  /*
   * Builds a complete mods-array entry for a mod: list responses do not
   * include the version, so it is resolved from the (cached) detail
   * endpoint. Falls back to what we already know if the lookup fails.
   */
  async function resolveModEntry(modId, name) {
    var entry = { modId: modId };
    if (name) entry.name = name;
    try {
      var mod = await fetchMod(modId, { maxRetries: 2 });
      if (mod.name) entry.name = mod.name;
      if (mod.version) entry.version = mod.version;
    } catch (e) { /* offline or still refreshing; entry stays partial */ }
    return entry;
  }

  /*
   * Turns a plain textarea into a JSON-highlighted editor: a highlight.js
   * layer sits behind the (transparent-text) textarea and stays in sync on
   * input and scroll.
   */
  function mountCodeEditor(textarea) {
    var wrap = document.createElement('div');
    wrap.className = 'code-editor';
    var pre = document.createElement('pre');
    pre.className = 'code-editor-highlight';
    pre.setAttribute('aria-hidden', 'true');
    var code = document.createElement('code');
    code.className = 'language-json';
    pre.appendChild(code);
    textarea.parentNode.insertBefore(wrap, textarea);
    wrap.appendChild(pre);
    wrap.appendChild(textarea);
    textarea.classList.add('code-editor-input');

    function sync() {
      pre.scrollTop = textarea.scrollTop;
      pre.scrollLeft = textarea.scrollLeft;
    }

    function refresh() {
      // Trailing newline keeps the highlight layer's height matching the
      // textarea when the last line is empty.
      code.textContent = textarea.value + '\n';
      if (window.hljs) {
        delete code.dataset.highlighted;
        window.hljs.highlightElement(code);
      }
      sync();
    }

    textarea.addEventListener('input', refresh);
    textarea.addEventListener('scroll', sync);
    refresh();
    return { refresh: refresh };
  }

  return {
    MOD_ID_RE: MOD_ID_RE,
    fetchJSON: fetchJSON,
    fetchJSONCached: fetchJSONCached,
    fetchMod: fetchMod,
    defaultConfig: defaultConfig,
    loadConfig: loadConfig,
    saveConfig: saveConfig,
    ensureConfig: ensureConfig,
    configMods: configMods,
    addModToConfig: addModToConfig,
    removeModAt: removeModAt,
    moveMod: moveMod,
    duplicateModIds: duplicateModIds,
    esc: esc,
    debounce: debounce,
    copyText: copyText,
    downloadFile: downloadFile,
    formatJSON: formatJSON,
    modSnippet: modSnippet,
    isModId: isModId,
    resolveModEntry: resolveModEntry,
    mountCodeEditor: mountCodeEditor
  };
})();
