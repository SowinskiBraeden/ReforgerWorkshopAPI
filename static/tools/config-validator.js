/* Config validator page: local JSON + structure checks, optional mod ID resolution. */
(function () {
  'use strict';

  var RM = window.RM;
  var input = document.getElementById('cv-input');
  var fileInput = document.getElementById('cv-file');
  var validateBtn = document.getElementById('cv-validate');
  var exampleBtn = document.getElementById('cv-example');
  var clearBtn = document.getElementById('cv-clear');
  var resultsEl = document.getElementById('cv-results');
  var summaryEl = document.getElementById('cv-summary');
  var modsEl = document.getElementById('cv-mods');
  if (!input) return;

  var MOD_CHECK_LIMIT = 40;
  var lastParsed = null;
  var editor = RM.mountCodeEditor(input);

  function renderSummary(counts) {
    if (!counts) {
      summaryEl.innerHTML = '';
      return;
    }
    var chips = [];
    var chip = function (cls, icon, text) {
      chips.push('<span class="badge ' + cls + ' border rounded-pill"><i class="bi ' + icon + '"></i> ' + text + '</span>');
    };
    if (counts.error) {
      chip('bg-danger-subtle border-danger-subtle text-danger-emphasis', 'bi-x-circle', counts.error + ' error' + (counts.error === 1 ? '' : 's'));
    }
    if (counts.warning) {
      chip('bg-warning-subtle border-warning-subtle text-warning-emphasis', 'bi-exclamation-triangle', counts.warning + ' warning' + (counts.warning === 1 ? '' : 's'));
    }
    if (counts.info) {
      chip('bg-secondary-subtle border-secondary-subtle text-secondary-emphasis', 'bi-info-circle', counts.info + ' note' + (counts.info === 1 ? '' : 's'));
    }
    if (!counts.error && !counts.warning) {
      chip('bg-success-subtle border-success-subtle text-success-emphasis', 'bi-check-lg', counts.info ? 'Valid with notes' : 'Valid config');
    }
    summaryEl.innerHTML = chips.join('');
  }

  function validate() {
    modsEl.innerHTML = '';
    lastParsed = null;
    var text = input.value;
    if (!text.trim()) {
      renderSummary(null);
      resultsEl.innerHTML = '<div class="finding finding-info"><i class="bi bi-info-circle"></i><span>Paste a config.json, upload a file, or load the example.</span></div>';
      return;
    }
    var parsed = RM.parseJSONWithPos(text);
    if (parsed.error) {
      var where = parsed.error.line
        ? ' (line ' + parsed.error.line + (parsed.error.col ? ', column ' + parsed.error.col : '') + ')'
        : '';
      renderSummary({ error: 1, warning: 0, info: 0 });
      resultsEl.innerHTML = '<div class="finding finding-error"><i class="bi bi-x-circle"></i><span><strong>Invalid JSON' +
        RM.esc(where) + ':</strong> ' + RM.esc(parsed.error.message) +
        '</span></div><div class="finding finding-info"><i class="bi bi-info-circle"></i><span>Common causes: trailing commas, comments, or curly quotes from a chat app. See the <a href="/guides/config-json-troubleshooting/">troubleshooting guide</a>.</span></div>';
      return;
    }
    lastParsed = parsed.value;
    renderSummary(RM.renderFindings(resultsEl, RM.validateConfig(parsed.value)));
    offerModCheck();
  }

  function offerModCheck() {
    var mods = (lastParsed && lastParsed.game && Array.isArray(lastParsed.game.mods)) ? lastParsed.game.mods : [];
    var ids = [];
    mods.forEach(function (m) {
      if (m && typeof m === 'object' && typeof m.modId === 'string') {
        var id = m.modId.trim().toUpperCase();
        if (RM.isModId(id) && ids.indexOf(id) === -1) ids.push(id);
      }
    });
    if (!ids.length) return;
    var over = ids.length > MOD_CHECK_LIMIT;
    modsEl.innerHTML = '<button type="button" class="btn btn-outline-secondary" id="cv-check-mods">' +
      '<i class="bi bi-search"></i> Check ' + Math.min(ids.length, MOD_CHECK_LIMIT) + ' mod ID' + (ids.length === 1 ? '' : 's') + ' against the Workshop</button>' +
      '<div class="tool-status">Sends only the mod IDs to the API, nothing else from your config.' +
      (over ? ' Only the first ' + MOD_CHECK_LIMIT + ' unique IDs are checked per run to respect API rate limits.' : '') + '</div>' +
      '<div id="cv-mod-results"></div>';
    document.getElementById('cv-check-mods').addEventListener('click', function () {
      checkMods(ids.slice(0, MOD_CHECK_LIMIT));
      this.disabled = true;
    });
  }

  async function checkMods(ids) {
    var target = document.getElementById('cv-mod-results');
    var rows = {};
    var renderTable = function () {
      target.innerHTML = '<table class="mod-list-table"><thead><tr><th>Mod ID</th><th>Status</th><th>Workshop name</th></tr></thead><tbody>' +
        ids.map(function (id) {
          var row = rows[id] || { status: '<span class="text-secondary">Waiting…</span>', name: '' };
          return '<tr><td><code>' + RM.esc(id) + '</code></td><td>' + row.status + '</td><td class="mod-list-name">' + RM.esc(row.name) + '</td></tr>';
        }).join('') + '</tbody></table>';
    };
    renderTable();
    for (var i = 0; i < ids.length; i++) {
      var id = ids[i];
      rows[id] = { status: '<span class="spinner-border spinner-border-sm"></span>', name: '' };
      renderTable();
      try {
        var mod = await RM.fetchMod(id, { maxRetries: 3 });
        rows[id] = { status: '<span class="text-success-emphasis"><i class="bi bi-check-circle"></i> Found</span>', name: mod.name || '' };
      } catch (err) {
        if (err.status === 404) {
          rows[id] = { status: '<span class="text-danger-emphasis"><i class="bi bi-x-circle"></i> Not found</span>', name: '' };
        } else if (err.stillRefreshing) {
          rows[id] = { status: '<span class="text-secondary"><i class="bi bi-clock"></i> Still refreshing, try again later</span>', name: '' };
        } else {
          rows[id] = { status: '<span class="text-warning-emphasis"><i class="bi bi-question-circle"></i> Unresolved (' + RM.esc(err.code || err.message) + ')</span>', name: '' };
        }
      }
      renderTable();
      if (i < ids.length - 1) await new Promise(function (r) { setTimeout(r, 350); });
    }
  }

  validateBtn.addEventListener('click', validate);
  input.addEventListener('input', RM.debounce(validate, 700));

  clearBtn.addEventListener('click', function () {
    input.value = '';
    editor.refresh();
    resultsEl.innerHTML = '';
    summaryEl.innerHTML = '';
    modsEl.innerHTML = '';
    lastParsed = null;
  });

  document.getElementById('cv-format').addEventListener('click', function () {
    var parsed = RM.parseJSONWithPos(input.value);
    if (parsed.error) {
      validate(); // surface the syntax error instead of silently doing nothing
      return;
    }
    input.value = JSON.stringify(parsed.value, null, 2);
    editor.refresh();
    validate();
  });

  exampleBtn.addEventListener('click', async function () {
    try {
      var res = await fetch('/static/tools/example-config.json');
      input.value = await res.text();
      editor.refresh();
      validate();
    } catch (e) {
      resultsEl.innerHTML = '<div class="finding finding-error"><i class="bi bi-x-circle"></i><span>Could not load the example config.</span></div>';
    }
  });

  fileInput.addEventListener('change', function () {
    var file = fileInput.files && fileInput.files[0];
    if (!file) return;
    var reader = new FileReader();
    reader.onload = function () {
      input.value = String(reader.result || '');
      editor.refresh();
      validate();
    };
    reader.readAsText(file);
    fileInput.value = '';
  });
})();
