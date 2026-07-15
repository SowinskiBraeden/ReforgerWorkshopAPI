/*
 * Inline Workshop mod search picker, used inside the config generator and
 * mod manager. Searches by name through /v1/search, or adds directly when
 * the query is a 16-hex Workshop ID. Adding resolves the mod's current
 * version from the detail endpoint (cached) so exported entries carry it.
 */
(function () {
  'use strict';

  var RM = window.RM;
  var MAX_RESULTS = 8;

  function mountModSearch(container, opts) {
    opts = opts || {};
    container.classList.add('mod-search');
    container.innerHTML =
      '<div class="mod-search-bar">' +
      '<input class="form-control" type="search" maxlength="120" autocomplete="off" ' +
      'placeholder="' + RM.esc(opts.placeholder || 'Search Workshop mods by name, or paste a mod ID...') + '" ' +
      'aria-label="Search Workshop mods">' +
      '</div>' +
      '<div class="mod-search-status tool-status" role="status"></div>' +
      '<div class="mod-search-results"></div>';

    var input = container.querySelector('input');
    var statusEl = container.querySelector('.mod-search-status');
    var resultsEl = container.querySelector('.mod-search-results');
    var seq = 0;
    var currentResults = [];

    function setStatus(html, isError) {
      statusEl.className = 'mod-search-status tool-status' + (isError ? ' tool-status-error' : '');
      statusEl.innerHTML = html;
    }

    function clearResults() {
      currentResults = [];
      resultsEl.innerHTML = '';
      setStatus('');
    }

    function inConfig(id) {
      var mods = RM.configMods(RM.ensureConfig());
      return mods.some(function (m) {
        return m && typeof m === 'object' && String(m.modId || '').toUpperCase() === id.toUpperCase();
      });
    }

    function rowHTML(mod) {
      var id = String(mod.ID || mod.id || '').toUpperCase();
      var added = inConfig(id);
      var thumb = mod.imageURL && mod.imageURL.indexOf('placeholder') === -1
        ? '<img class="mod-search-thumb" src="' + RM.esc(mod.imageURL) + '" alt="" loading="lazy">'
        : '<span class="mod-search-thumb mod-search-thumb-empty"><i class="bi bi-box-seam"></i></span>';
      var metaBits = [];
      if (mod.author) metaBits.push(RM.esc(mod.author));
      if (mod.size) metaBits.push(RM.esc(mod.size));
      var btn = added
        ? '<button type="button" class="btn btn-sm btn-outline-secondary" disabled>In config</button>'
        : '<button type="button" class="btn btn-sm btn-primary ms-add"><i class="bi bi-plus-lg"></i> Add</button>';
      return '<div class="mod-search-row" data-mod-id="' + RM.esc(id) + '" data-mod-name="' + RM.esc(mod.name || '') + '">' +
        thumb +
        '<div class="mod-search-info">' +
        '<a class="mod-search-name" href="/mods/' + RM.esc(id) + '/" title="Open mod details">' + RM.esc(mod.name || id) + '</a>' +
        '<span class="mod-search-meta">' + metaBits.join(' &middot; ') + '</span>' +
        '</div>' +
        btn +
        '</div>';
    }

    function renderResults(mods) {
      currentResults = mods;
      if (!mods.length) {
        resultsEl.innerHTML = '';
        return;
      }
      resultsEl.innerHTML = mods.map(rowHTML).join('');
      resultsEl.querySelectorAll('.ms-add').forEach(function (btn) {
        btn.addEventListener('click', function () {
          var row = btn.closest('.mod-search-row');
          addMod(row.getAttribute('data-mod-id'), row.getAttribute('data-mod-name'), btn);
        });
      });
    }

    /* Adding resolves version (and name, for by-ID adds) from the detail
       endpoint first, so the entry lands complete in the config. */
    async function addMod(id, name, btn) {
      var original = btn.innerHTML;
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
      var entry = { modId: id, name: name || '' };
      try {
        var mod = await RM.fetchMod(id, { maxRetries: 3 });
        if (mod.name) entry.name = mod.name;
        if (mod.version) entry.version = mod.version;
      } catch (err) {
        if (err.status === 404) {
          btn.disabled = false;
          btn.innerHTML = original;
          setStatus('No Workshop mod exists with ID ' + RM.esc(id) + '.', true);
          return;
        }
        // Version lookup failed (offline, still refreshing); add without it.
      }
      var result = RM.addModToConfig(entry);
      btn.className = 'btn btn-sm btn-outline-secondary';
      btn.innerHTML = result.added ? '<i class="bi bi-check-lg"></i> Added' : 'In config';
      if (result.added && !entry.version) {
        setStatus('Added ' + RM.esc(entry.name || id) + '. Version could not be resolved right now; use Resolve all mods later.');
      } else if (result.added) {
        setStatus('Added ' + RM.esc(entry.name || id) + (entry.version ? ' (v' + RM.esc(entry.version) + ')' : '') + '.');
      }
    }

    async function search() {
      var query = input.value.trim();
      if (!query) {
        clearResults();
        return;
      }
      var mySeq = ++seq;

      // A pasted Workshop ID gets a direct add row; its real name and
      // version are resolved when Add is clicked.
      if (RM.isModId(query)) {
        setStatus('');
        renderResults([{ ID: query.toUpperCase(), name: '' }]);
        return;
      }

      if (query.length < 2) return;
      setStatus('<span class="spinner-border" role="status" aria-hidden="true"></span>Searching…');
      try {
        var result = await RM.fetchJSONCached('/v1/search?search=' + encodeURIComponent(query), {
          onWait: function () {
            if (mySeq === seq) setStatus('<span class="spinner-border" role="status" aria-hidden="true"></span>Fetching fresh Workshop data…');
          }
        });
        if (mySeq !== seq) return;
        var mods = (result.data && result.data.data) || [];
        renderResults(mods.slice(0, MAX_RESULTS));
        setStatus(mods.length ? '' : 'No mods found for "' + RM.esc(query) + '".');
      } catch (err) {
        if (mySeq !== seq) return;
        renderResults([]);
        if (err.code === 'NOT_FOUND' || err.status === 404) {
          setStatus('No mods found for "' + RM.esc(query) + '".');
        } else if (err.stillRefreshing) {
          setStatus('Workshop data is still being fetched. Try again in a moment.');
        } else {
          setStatus('Search failed: ' + RM.esc(err.message), true);
        }
      }
    }

    input.addEventListener('input', RM.debounce(search, 400));
    input.addEventListener('keydown', function (event) {
      if (event.key === 'Enter') {
        event.preventDefault();
        search();
      }
      if (event.key === 'Escape') {
        input.value = '';
        clearResults();
      }
    });

    // Keep "In config" states in sync when the list changes elsewhere.
    window.addEventListener('rm-config-changed', function () {
      if (currentResults.length) renderResults(currentResults);
    });

    return { clear: clearResults };
  }

  RM.mountModSearch = mountModSearch;
})();
