/* Mod detail page: loads /v1/mod/{id} and renders full mod information. */
(function () {
  'use strict';

  var RM = window.RM;
  var root = document.getElementById('mod-detail');
  var statusEl = document.getElementById('md-status');
  var contentEl = document.getElementById('md-content');
  if (!root) return;

  var modId = root.getAttribute('data-mod-id') || '';

  function setStatus(html, isError) {
    statusEl.className = 'tool-status' + (isError ? ' tool-status-error' : '');
    statusEl.innerHTML = html;
  }

  function spinner(text) {
    return '<span class="spinner-border" role="status" aria-hidden="true"></span>' + RM.esc(text);
  }

  async function load() {
    setStatus(spinner('Loading mod details…'));
    try {
      var mod = await RM.fetchMod(modId, {
        onWait: function () { setStatus(spinner('Fetching fresh Workshop data…')); }
      });
      setStatus('');
      render(mod);
    } catch (err) {
      if (err.status === 404) {
        setStatus('');
        contentEl.innerHTML = '<div class="mod-list-empty">No Workshop mod was found for ID <code>' + RM.esc(modId) + '</code>.' +
          '<br>Search for it in the <a href="/mods/">mod browser</a> instead.</div>';
      } else if (err.stillRefreshing) {
        setStatus('Workshop data is still being fetched. <button type="button" class="btn btn-sm btn-outline-secondary" id="md-retry">Try again</button>');
        document.getElementById('md-retry').addEventListener('click', load);
      } else {
        setStatus('Could not load this mod: ' + RM.esc(err.message) + ' <button type="button" class="btn btn-sm btn-outline-secondary" id="md-retry">Retry</button>', true);
        document.getElementById('md-retry').addEventListener('click', load);
      }
    }
  }

  function fact(label, value) {
    if (value === undefined || value === null || value === '' || value === 0) return '';
    return '<tr><th scope="row">' + RM.esc(label) + '</th><td>' + RM.esc(typeof value === 'number' ? value.toLocaleString() : value) + '</td></tr>';
  }

  function dependencyId(dep) {
    // Dependency links carry the mod ID as the last URL path segment.
    var url = dep.apiModURL || dep.originalModURL || '';
    var segment = url.split('/').filter(Boolean).pop() || '';
    var idPart = segment.split('-')[0];
    return RM.isModId(idPart) ? idPart.toUpperCase() : '';
  }

  function dependencyCard(dep) {
    var depId = dependencyId(dep);
    var title = dep.name || depId || 'Unknown dependency';
    var href = depId ? '/mods/' + depId + '/' : (dep.originalModURL || '');
    var meta = depId ? '<code>' + RM.esc(depId) + '</code>' : '<span class="text-secondary">No ID available</span>';
    var body = '<div class="dependency-card-body"><strong>' + RM.esc(title) + '</strong><span>' + meta + '</span></div>';
    if (href) {
      return '<a class="dependency-card" href="' + RM.esc(href) + '"' + (depId ? '' : ' target="_blank" rel="noopener"') + '>' + body + '<i class="bi bi-chevron-right"></i></a>';
    }
    return '<div class="dependency-card">' + body + '</div>';
  }

  function render(mod) {
    // Reflect the real mod name in the document metadata.
    if (mod.name) {
      document.title = mod.name + ' - Arma Reforger Mod ' + modId + ' | Reforger Mods API';
      var h1 = document.querySelector('h1');
      if (h1) h1.textContent = mod.name;
      var crumb = document.querySelector('.tool-breadcrumb span:last-child');
      if (crumb) crumb.textContent = mod.name;
    }

    var configEntry = '<section class="mod-detail-section mod-detail-config"><h2>config.json entry</h2><pre class="tool-preview mod-detail-snippet"><code id="md-snippet" class="language-json">' + RM.esc(RM.modSnippet({ modId: modId, name: mod.name, version: mod.version })) + '</code></pre></section>';
    var html = '<div class="mod-detail-layout"><section class="mod-detail-main"><div class="mod-detail-hero">';
    if (mod.imageURL) {
      html += '<img class="mod-detail-image" src="' + RM.esc(mod.imageURL) + '" alt="' + RM.esc(mod.name) + ' preview">';
    }
    html += '</div>';

    if (mod.summary) html += '<section class="mod-detail-section"><h2>Summary</h2><p class="mod-summary-box">' + RM.esc(mod.summary) + '</p></section>';
    if (mod.description && mod.description !== mod.summary) {
      html += '<section class="mod-detail-section"><h2>Official Workshop description</h2><div class="mod-official-description">' + RM.esc(mod.description) + '</div></section>';
    }

    if (mod.scenarios && mod.scenarios.length) {
      html += '<section class="mod-detail-section"><h2>Scenarios</h2><p>Scenario IDs for <code>game.scenarioId</code> in your server config:</p>';
      mod.scenarios.forEach(function (scenario) {
        html += '<div class="scenario-card"><strong>' + RM.esc(scenario.name || 'Scenario') + '</strong>';
        var bits = [];
        if (scenario.gamemode) bits.push(RM.esc(scenario.gamemode));
        if (scenario.playerCount) bits.push(RM.esc(scenario.playerCount) + ' players');
        if (bits.length) html += ' <span class="mod-card-meta d-inline">' + bits.join(' · ') + '</span>';
        if (scenario.description) html += '<br>' + RM.esc(scenario.description);
        if (scenario.scenarioID) {
          html += '<br><code>' + RM.esc(scenario.scenarioID) + '</code> ' +
            '<button type="button" class="btn btn-link btn-sm p-0 md-copy-scenario" data-scenario="' + RM.esc(scenario.scenarioID) + '" title="Copy scenario ID"><i class="bi bi-clipboard"></i></button>';
        }
        html += '</div>';
      });
      html += '</section>';
    }

    html += configEntry + '</section>';

    html += '<aside class="mod-detail-sidebar"><table class="mod-detail-facts-table"><tbody>' +
      fact('Author', mod.author) +
      fact('Version', mod.version) +
      fact('Game version', mod.gameVersion) +
      fact('Size', mod.size) +
      fact('Rating', mod.rating) +
      fact('Subscribers', mod.subscribers) +
      fact('Downloads', mod.downloads) +
      fact('Created', mod.created) +
      fact('Last modified', mod.lastModified) +
      fact('License', mod.license) +
      ((mod.dependencies && mod.dependencies.length) ? fact('Dependencies', mod.dependencies.length) : '') +
      fact('ID', modId) +
      '</tbody></table>';

    html += '<div class="mod-detail-actions">' +
      '<button type="button" class="btn btn-primary" id="md-add">Add to Config</button>' +
      '<button type="button" class="btn btn-outline-secondary" id="md-copy-id">Copy mod ID</button>' +
      '<button type="button" class="btn btn-outline-secondary" id="md-copy-json">Copy JSON entry</button>' +
      (mod.originalModURL ? '<a class="btn btn-outline-secondary" href="' + RM.esc(mod.originalModURL) + '" target="_blank" rel="noopener"><i class="bi bi-box-arrow-up-right"></i> Official Workshop</a>' : '') +
      '</div>';

    html += configEntry.replace('id="md-snippet"', 'id="md-snippet-mobile"');

    if (mod.tags && mod.tags.length) {
      html += '<div class="mod-detail-tags">' + mod.tags.map(function (tag) {
        return '<span class="badge bg-secondary-subtle text-secondary-emphasis border border-secondary-subtle me-1">' + RM.esc(tag) + '</span>';
      }).join('') + '</div>';
    }

    if (mod.dependencies && mod.dependencies.length) {
      html += '<section class="mod-detail-side-section"><h2>Dependencies</h2><p>Make sure these are in <code>game.mods</code> too.</p><div class="dependency-card-list">';
      mod.dependencies.forEach(function (dep) {
        html += dependencyCard(dep);
      });
      html += '</div></section>';
    }

    html += '</aside></div>';

    contentEl.innerHTML = html;
    var snippets = contentEl.querySelectorAll('#md-snippet, #md-snippet-mobile');
    if (window.hljs) {
      snippets.forEach(function (snippet) {
        window.hljs.highlightElement(snippet);
      });
    }

    document.getElementById('md-copy-id').addEventListener('click', function () {
      RM.copyText(modId, this);
    });
    document.getElementById('md-copy-json').addEventListener('click', function () {
      RM.copyText(RM.modSnippet({ modId: modId, name: mod.name, version: mod.version }), this);
    });
    document.getElementById('md-add').addEventListener('click', function () {
      var btn = this;
      var res = RM.addModToConfig({ modId: modId, name: mod.name, version: mod.version });
      btn.innerHTML = res.added ? '<i class="bi bi-check-lg"></i> Added to config' : 'Already in config';
      btn.disabled = true;
      setTimeout(function () { btn.innerHTML = 'Add to Config'; btn.disabled = false; }, 1800);
    });
    contentEl.querySelectorAll('.md-copy-scenario').forEach(function (btn) {
      btn.addEventListener('click', function () { RM.copyText(btn.getAttribute('data-scenario'), btn); });
    });
  }

  load();
})();
