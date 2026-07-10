/* Mod manager page: focused editor for game.mods of the working config. */
(function () {
  'use strict';

  var RM = window.RM;
  var listEl = document.getElementById('mm-list');
  var statusEl = document.getElementById('mm-status');
  var depsResults = document.getElementById('mm-deps-results');
  if (!listEl) return;

  function setStatus(html, isError) {
    statusEl.className = 'tool-status' + (isError ? ' tool-status-error' : '');
    statusEl.innerHTML = html;
  }

  RM.mountModSearch(document.getElementById('mm-search'), {
    placeholder: 'Search Workshop mods to add, or paste a mod ID...'
  });
  var modList = RM.mountModList(listEl, {});

  document.getElementById('mm-resolve').addEventListener('click', async function () {
    var btn = this;
    btn.disabled = true;
    try {
      var result = await modList.resolveAll(function (done, total) {
        setStatus('<span class="spinner-border" role="status"></span>Resolving mods… ' + done + ' of ' + total);
      });
      var parts = [];
      if (result.resolvedCount) parts.push(result.resolvedCount + ' resolved');
      if (result.failed.length) parts.push(result.failed.length + ' not found or unavailable');
      if (result.skipped) parts.push(result.skipped + ' skipped to respect rate limits');
      setStatus(parts.length ? '<i class="bi bi-check-lg"></i> ' + parts.join(', ') + '.' : 'Everything is already resolved.');
    } catch (e) {
      setStatus('Resolving failed: ' + RM.esc(e.message), true);
    } finally {
      btn.disabled = false;
    }
  });

  document.getElementById('mm-deps').addEventListener('click', async function () {
    var btn = this;
    btn.disabled = true;
    depsResults.innerHTML = '';
    try {
      var result = await modList.checkDependencies(function (done, total) {
        setStatus('<span class="spinner-border" role="status"></span>Checking dependencies… resolving mod ' + done + ' of ' + total);
      });
      setStatus('');
      renderDeps(result);
    } catch (e) {
      setStatus('Dependency check failed: ' + RM.esc(e.message), true);
    } finally {
      btn.disabled = false;
    }
  });

  function renderDeps(result) {
    var html = '';
    if (!result.missing.length) {
      html += '<div class="finding finding-ok"><i class="bi bi-check-circle"></i><span>No missing dependencies found in the Workshop data for your mods.</span></div>';
    } else {
      html += '<div class="finding finding-warning"><i class="bi bi-exclamation-triangle"></i><span>' +
        result.missing.length + ' dependency suggestion' + (result.missing.length === 1 ? '' : 's') +
        '. Workshop dependency data can lag behind mod updates, so review before adding.</span></div>';
      html += '<table class="mod-list-table"><thead><tr><th>Dependency</th><th>Required by</th><th></th></tr></thead><tbody>' +
        result.missing.map(function (dep, idx) {
          return '<tr><td class="mod-list-name">' + RM.esc(dep.name) + (dep.id ? ' <code>' + RM.esc(dep.id) + '</code>' : '') + '</td>' +
            '<td>' + RM.esc(dep.requiredBy) + '</td>' +
            '<td class="mod-list-actions">' + (dep.id
              ? '<button type="button" class="btn btn-outline-secondary mm-dep-add" data-id="' + RM.esc(dep.id) + '" data-name="' + RM.esc(dep.name) + '" data-idx="' + idx + '"><i class="bi bi-plus-lg"></i> Add</button>'
              : '<span class="text-secondary">No ID available</span>') + '</td></tr>';
        }).join('') + '</tbody></table>';
    }
    if (result.unresolved.length) {
      html += '<div class="finding finding-info"><i class="bi bi-info-circle"></i><span>' + result.unresolved.length +
        ' mod' + (result.unresolved.length === 1 ? '' : 's') + ' could not be resolved, so their dependencies were not checked.</span></div>';
    }
    depsResults.innerHTML = html;
    depsResults.querySelectorAll('.mm-dep-add').forEach(function (btn) {
      btn.addEventListener('click', function () {
        var added = RM.addModToConfig({ modId: btn.getAttribute('data-id'), name: btn.getAttribute('data-name') });
        btn.innerHTML = added.added ? '<i class="bi bi-check-lg"></i> Added' : 'Already in config';
        btn.disabled = true;
      });
    });
  }

  document.getElementById('mm-copy-mods').addEventListener('click', function () {
    RM.copyText(JSON.stringify(RM.configMods(RM.ensureConfig()), null, 2), this);
  });
  document.getElementById('mm-download').addEventListener('click', function () {
    RM.downloadFile('config.json', RM.formatJSON(RM.ensureConfig()));
  });
})();
