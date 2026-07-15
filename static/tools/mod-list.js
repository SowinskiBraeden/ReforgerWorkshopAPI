/*
 * Shared mods-array editor. Renders and edits game.mods of the working
 * config (RM.ensureConfig). Used by the mod manager page and the config
 * generator, so both always show the same list.
 */
(function () {
  'use strict';

  var RM = window.RM;
  var resolved = {}; // modId -> mod detail (runtime memo on top of sessionStorage cache)
  var RESOLVE_LIMIT = 40; // stays well inside the anonymous rate limit
  var RESOLVE_GAP_MS = 350;

  function sleep(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }

  function mountModList(container, opts) {
    opts = opts || {};

    function notify() {
      if (opts.onChange) opts.onChange();
    }

    function render() {
      var mods = RM.configMods(RM.ensureConfig());
      if (!mods.length) {
        container.innerHTML = '<div class="mod-list-empty">No mods in your working config yet.<br>' +
          'Add them from the <a href="/mods/">mod browser</a> or by Workshop ID.</div>';
        return;
      }
      var dupes = RM.duplicateModIds(mods);
      var rows = mods.map(function (mod, idx) {
        var entry = (mod && typeof mod === 'object') ? mod : {};
        var id = String(entry.modId || '').trim();
        var upper = id.toUpperCase();
        var isDupe = dupes[upper];
        var detail = resolved[upper];
        var name = entry.name || (detail && detail.name) || '';
        var resolvedNote = '';
        if (detail && entry.name && detail.name && detail.name !== entry.name) {
          resolvedNote = '<span class="mod-list-resolved">Workshop name: ' + RM.esc(detail.name) + '</span>';
        }
        var badId = id && !RM.isModId(id);
        return '<tr class="' + (isDupe ? 'mod-list-row-duplicate' : '') + '" data-index="' + idx + '">' +
          '<td class="mod-list-name">' + (name ? RM.esc(name) : '<span class="text-secondary">Unresolved</span>') + resolvedNote +
          (isDupe ? '<span class="mod-list-resolved text-warning-emphasis"><i class="bi bi-exclamation-triangle"></i> Duplicate mod ID</span>' : '') +
          (badId ? '<span class="mod-list-resolved text-warning-emphasis"><i class="bi bi-exclamation-triangle"></i> Not a 16-character Workshop ID</span>' : '') +
          '</td>' +
          '<td><code>' + RM.esc(id) + '</code></td>' +
          '<td>' + (entry.version ? RM.esc(entry.version) : '<span class="text-secondary">latest</span>') + '</td>' +
          '<td class="mod-list-actions">' +
          '<button type="button" class="btn btn-outline-secondary ml-up" title="Move up" aria-label="Move up"' + (idx === 0 ? ' disabled' : '') + '><i class="bi bi-arrow-up"></i></button>' +
          '<button type="button" class="btn btn-outline-secondary ml-down" title="Move down" aria-label="Move down"' + (idx === mods.length - 1 ? ' disabled' : '') + '><i class="bi bi-arrow-down"></i></button>' +
          '<button type="button" class="btn btn-outline-secondary ml-copy" title="Copy mod ID" aria-label="Copy mod ID"><i class="bi bi-clipboard"></i></button>' +
          '<button type="button" class="btn btn-outline-danger ml-remove" title="Remove" aria-label="Remove"><i class="bi bi-trash"></i></button>' +
          '</td></tr>';
      }).join('');
      container.innerHTML = '<table class="mod-list-table"><thead><tr>' +
        '<th>Mod</th><th>ID</th><th>Version</th><th></th></tr></thead><tbody>' + rows + '</tbody></table>';
      bind();
    }

    function bind() {
      container.querySelectorAll('tr[data-index]').forEach(function (row) {
        var idx = parseInt(row.getAttribute('data-index'), 10);
        var mods = RM.configMods(RM.ensureConfig());
        var id = mods[idx] && mods[idx].modId ? String(mods[idx].modId) : '';
        row.querySelector('.ml-up').addEventListener('click', function () {
          RM.moveMod(idx, -1); render(); notify();
        });
        row.querySelector('.ml-down').addEventListener('click', function () {
          RM.moveMod(idx, 1); render(); notify();
        });
        row.querySelector('.ml-remove').addEventListener('click', function () {
          RM.removeModAt(idx); render(); notify();
        });
        var copyBtn = row.querySelector('.ml-copy');
        copyBtn.addEventListener('click', function () { RM.copyText(id, copyBtn); });
      });
    }

    /*
     * Resolve names/details for the mods in the list, sequentially and
     * spaced out, so a long list cannot hammer the API. Fills empty name
     * fields in the working config from Workshop data.
     */
    async function resolveAll(progress) {
      var cfg = RM.ensureConfig();
      var mods = RM.configMods(cfg);
      var ids = [];
      mods.forEach(function (m) {
        if (!m || typeof m !== 'object') return;
        var id = String(m.modId || '').trim().toUpperCase();
        if (RM.isModId(id) && !resolved[id] && ids.indexOf(id) === -1) ids.push(id);
      });
      var skipped = ids.length > RESOLVE_LIMIT ? ids.length - RESOLVE_LIMIT : 0;
      ids = ids.slice(0, RESOLVE_LIMIT);
      var failed = [];
      for (var i = 0; i < ids.length; i++) {
        if (progress) progress(i + 1, ids.length, ids[i]);
        try {
          resolved[ids[i]] = await RM.fetchMod(ids[i], { maxRetries: 3 });
        } catch (err) {
          failed.push({ id: ids[i], status: err.status, stillRefreshing: err.stillRefreshing });
        }
        if (i < ids.length - 1) await sleep(RESOLVE_GAP_MS);
      }
      // Fill in missing names and versions from resolved data (never
      // overwrite values the user already set).
      var changed = false;
      mods.forEach(function (m) {
        if (!m || typeof m !== 'object') return;
        var id = String(m.modId || '').trim().toUpperCase();
        if (!resolved[id]) return;
        if (!m.name && resolved[id].name) {
          m.name = resolved[id].name;
          changed = true;
        }
        if (!m.version && resolved[id].version) {
          m.version = resolved[id].version;
          changed = true;
        }
      });
      if (changed) RM.saveConfig(cfg);
      render();
      if (changed) notify();
      return { resolvedCount: ids.length - failed.length, failed: failed, skipped: skipped };
    }

    function dependencyIdFromLink(dep) {
      var url = dep.apiModURL || dep.originalModURL || '';
      var segment = url.split('/').filter(Boolean).pop() || '';
      var idPart = segment.split('-')[0];
      return RM.isModId(idPart) ? idPart.toUpperCase() : '';
    }

    /*
     * Compare resolved dependency data against the current list. Returns
     * suggestions only; dependency data from public Workshop pages can lag,
     * so nothing is added automatically.
     */
    async function checkDependencies(progress) {
      var result = await resolveAll(progress);
      var mods = RM.configMods(RM.ensureConfig());
      var present = {};
      mods.forEach(function (m) {
        if (m && typeof m === 'object' && m.modId) present[String(m.modId).toUpperCase()] = true;
      });
      var missing = [];
      var seen = {};
      Object.keys(resolved).forEach(function (id) {
        if (!present[id]) return; // only check deps of mods actually in the list
        (resolved[id].dependencies || []).forEach(function (dep) {
          var depId = dependencyIdFromLink(dep);
          var key = depId || (dep.name || '');
          if (!key || seen[key]) return;
          seen[key] = true;
          if (depId && present[depId]) return;
          missing.push({ id: depId, name: dep.name || depId, requiredBy: resolved[id].name || id });
        });
      });
      return { missing: missing, unresolved: result.failed, skipped: result.skipped };
    }

    // Refresh when another tab or another tool page changes the config.
    window.addEventListener('rm-config-changed', render);
    window.addEventListener('storage', render);

    render();
    return { render: render, resolveAll: resolveAll, checkDependencies: checkDependencies };
  }

  RM.mountModList = mountModList;
})();
