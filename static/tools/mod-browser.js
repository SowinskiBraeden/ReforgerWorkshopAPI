/* Mod browser page: search, sort, paginate /v1/mods and render result cards. */
(function () {
  'use strict';

  var RM = window.RM;
  var form = document.getElementById('mb-form');
  var searchInput = document.getElementById('mb-search');
  var categoryButtons = Array.prototype.slice.call(document.querySelectorAll('#mb-category-group [data-category]'));
  var sortSelect = document.getElementById('mb-sort');
  var statusEl = document.getElementById('mb-status');
  var resultsEl = document.getElementById('mb-results');
  var paginationEl = document.getElementById('mb-pagination');
  if (!form) return;

  var state = readStateFromURL();
  var requestSeq = 0;

  function readStateFromURL() {
    var params = new URLSearchParams(window.location.search);
    var page = parseInt(params.get('page'), 10);
    var sort = params.get('sort') || '';
    var category = (params.get('category') || params.get('tags') || '').trim().toUpperCase();
    var view = params.get('view') || localStorage.getItem('rm.modBrowser.view') || 'card';
    var validSorts = ['popularity', 'newest', 'subscribers', 'version_size'];
    var validViews = ['card', 'list'];
    return {
      search: (params.get('search') || '').trim().slice(0, 120),
      category: category.slice(0, 40),
      sort: validSorts.indexOf(sort) !== -1 ? sort : '',
      page: page > 0 ? page : 1,
      view: validViews.indexOf(view) !== -1 ? view : 'card'
    };
  }

  /* Canonical param order (search, sort, page) with defaults omitted, so
     reordered or redundant params never create a distinct browser state. */
  function stateToQuery(s) {
    var params = new URLSearchParams();
    if (s.search) params.set('search', s.search);
    if (s.category) params.set('category', s.category);
    if (s.sort) params.set('sort', s.sort);
    if (s.page > 1) params.set('page', String(s.page));
    if (s.view === 'list') params.set('view', s.view);
    var q = params.toString();
    return q ? '?' + q : '';
  }

  function pushState(replace) {
    var url = window.location.pathname + stateToQuery(state);
    if (replace) {
      history.replaceState(null, '', url);
    } else {
      history.pushState(null, '', url);
    }
  }

  function apiPath() {
    var params = new URLSearchParams();
    if (state.search) params.set('search', state.search);
    if (state.category) params.set('tags', state.category);
    if (state.sort) params.set('sort', state.sort);
    var q = params.toString();
    return '/v1/mods/' + state.page + (q ? '?' + q : '');
  }

  function setStatus(html, isError) {
    statusEl.className = 'tool-status' + (isError ? ' tool-status-error' : '');
    statusEl.innerHTML = html;
  }

  function spinner(text) {
    return '<span class="spinner-border" role="status" aria-hidden="true"></span>' + RM.esc(text);
  }

  function renderSkeletons() {
    applyViewClass();
    var card = '<div class="mod-card-skeleton" aria-hidden="true"><div class="skeleton-block skeleton-image"></div>' +
      '<div class="skeleton-body"><div class="skeleton-block skeleton-line w-75"></div>' +
      '<div class="skeleton-block skeleton-line w-50"></div><div class="skeleton-block skeleton-line"></div></div></div>';
    resultsEl.innerHTML = new Array(8 + 1).join(card);
  }

  async function load() {
    var seq = ++requestSeq;
    setStatus(spinner('Loading mods…'));
    renderSkeletons();
    paginationEl.innerHTML = '';
    try {
      var result;
      if (RM.isModId(state.search)) {
        result = await loadSingleMod(state.search, seq);
      } else {
        result = await RM.fetchJSONCached(apiPath(), {
          onWait: function () {
            if (seq === requestSeq) setStatus(spinner('Fetching fresh Workshop data…'));
          }
        });
      }
      if (seq !== requestSeq) return;
      render(result);
    } catch (err) {
      if (seq !== requestSeq) return;
      resultsEl.innerHTML = '';
      paginationEl.innerHTML = '';
      if (err.code === 'NOT_FOUND' || err.status === 404) {
        setStatus('');
        resultsEl.innerHTML = '<div class="mod-list-empty">No mods found' +
          (state.search ? ' for <strong>' + RM.esc(state.search) + '</strong>' : '') +
          '. Try a shorter search term.</div>';
      } else if (err.stillRefreshing) {
        setStatus('Workshop data is still being fetched. <button type="button" class="btn btn-sm btn-outline-secondary" id="mb-retry">Try again</button>');
        var retry = document.getElementById('mb-retry');
        if (retry) retry.addEventListener('click', load);
      } else {
        setStatus('Could not load mods: ' + RM.esc(err.message) + ' <button type="button" class="btn btn-sm btn-outline-secondary" id="mb-retry">Retry</button>', true);
        var retryBtn = document.getElementById('mb-retry');
        if (retryBtn) retryBtn.addEventListener('click', load);
      }
    }
  }

  async function loadSingleMod(id, seq) {
    var mod = await RM.fetchMod(id, {
      onWait: function () {
        if (seq === requestSeq) setStatus(spinner('Fetching fresh Workshop data…'));
      }
    });
    return {
      data: {
        data: [mod],
        meta: {
          totalMods: 1,
          shownMods: 1,
          currentPage: 1,
          totalPages: 1,
          modsIndexStart: 1,
          modsIndexEnd: 1
        }
      },
      cache: ''
    };
  }

  function render(result) {
    var body = result.data || {};
    var mods = body.data || [];
    var meta = body.meta || {};
    applyViewClass();

    var summary = '';
    if (meta.totalMods) {
      summary = 'Showing <strong>' + RM.esc((meta.modsIndexStart || 1).toLocaleString()) + '&ndash;' +
        RM.esc((meta.modsIndexEnd || mods.length).toLocaleString()) + '</strong> of <strong>' +
        RM.esc(meta.totalMods.toLocaleString()) + '</strong> mods' +
        (state.search ? ' for <strong>' + RM.esc(state.search) + '</strong>' : '') +
        (state.category ? ' in <strong>' + RM.esc(categoryLabel(state.category)) + '</strong>' : '');
    }
    if (result.cache === 'STALE') {
      setStatus((summary ? summary + ' &middot; ' : '') + '<i class="bi bi-clock-history"></i> cached results, refreshing in the background');
    } else {
      setStatus(summary);
    }

    if (!mods.length) {
      resultsEl.innerHTML = '<div class="mod-list-empty">No mods found.</div>';
      paginationEl.innerHTML = '';
      return;
    }

    resultsEl.innerHTML = mods.map(cardHTML).join('');
    bindCards();
    renderPagination(meta);
  }

  function categoryLabel(value) {
    for (var i = 0; i < categoryButtons.length; i++) {
      if (categoryButtons[i].getAttribute('data-category') === value) return categoryButtons[i].textContent;
    }
    return value;
  }

  function syncCategoryButtons() {
    categoryButtons.forEach(function (btn) {
      var active = btn.getAttribute('data-category') === state.category;
      btn.classList.toggle('active', active);
      btn.setAttribute('aria-pressed', active ? 'true' : 'false');
    });
  }

  function applyViewClass() {
    resultsEl.className = state.view === 'list' ? 'mod-grid mod-grid-list' : 'mod-grid';
    document.querySelectorAll('.mb-view-toggle [data-view]').forEach(function (btn) {
      var active = btn.getAttribute('data-view') === state.view;
      btn.classList.toggle('active', active);
      btn.setAttribute('aria-pressed', active ? 'true' : 'false');
    });
  }

  function detailPath(id) {
    return '/arma-reforger-mods/' + encodeURIComponent(String(id).toUpperCase()) + '/';
  }

  function cardHTML(mod) {
    var id = mod.ID || mod.id || '';
    var image = mod.imageURL
      ? '<img class="mod-card-image" src="' + RM.esc(mod.imageURL) + '" alt="" loading="lazy" referrerpolicy="no-referrer">'
      : '<div class="mod-card-image-placeholder"><i class="bi bi-box-seam"></i></div>';
    var metaBits = [];
    if (mod.author) metaBits.push('<span><i class="bi bi-person"></i> ' + RM.esc(mod.author) + '</span>');
    if (mod.size) metaBits.push('<span><i class="bi bi-hdd"></i> ' + RM.esc(mod.size) + '</span>');
    if (mod.rating) metaBits.push('<span><i class="bi bi-hand-thumbs-up"></i> ' + RM.esc(mod.rating) + '</span>');
    return '<article class="mod-card" data-mod-id="' + RM.esc(id) + '" data-mod-name="' + RM.esc(mod.name || '') + '">' +
      '<a href="' + detailPath(id) + '" tabindex="-1" aria-hidden="true">' + image + '</a>' +
      '<div class="mod-card-body">' +
      '<h3 class="mod-card-title"><a href="' + detailPath(id) + '">' + RM.esc(mod.name || id) + '</a></h3>' +
      '<div class="mod-card-meta">' + metaBits.join('') + '</div>' +
      '<div class="mod-card-id"><code>' + RM.esc(id) + '</code>' +
      '<button type="button" class="btn btn-link btn-sm p-0 mb-copy-id" title="Copy mod ID" aria-label="Copy mod ID"><i class="bi bi-clipboard"></i></button></div>' +
      '<div class="mod-card-actions">' +
      '<button type="button" class="btn btn-primary mb-add">Add to Config</button>' +
      '<button type="button" class="btn btn-outline-secondary mb-copy-json" title="Copy config.json mods entry">Copy JSON</button>' +
      (mod.originalModURL ? '<a class="btn btn-outline-secondary" href="' + RM.esc(mod.originalModURL) + '" target="_blank" rel="noopener" title="Open on the official Workshop"><i class="bi bi-box-arrow-up-right"></i></a>' : '') +
      '</div></div></article>';
  }

  function bindCards() {
    resultsEl.querySelectorAll('.mod-card-image').forEach(function (img) {
      if (img.complete && img.naturalWidth > 0) {
        img.classList.add('is-loaded');
        return;
      }
      img.addEventListener('load', function () { img.classList.add('is-loaded'); });
      img.addEventListener('error', function () {
        var placeholder = document.createElement('div');
        placeholder.className = 'mod-card-image-placeholder';
        placeholder.innerHTML = '<i class="bi bi-box-seam"></i>';
        img.replaceWith(placeholder);
      });
    });
    resultsEl.querySelectorAll('.mod-card').forEach(function (card) {
      var id = card.getAttribute('data-mod-id');
      var name = card.getAttribute('data-mod-name');
      var copyId = card.querySelector('.mb-copy-id');
      var copyJson = card.querySelector('.mb-copy-json');
      var addBtn = card.querySelector('.mb-add');
      if (copyId) copyId.addEventListener('click', function () { RM.copyText(id, copyId); });
      // List responses carry no version, so both actions resolve the entry
      // through the cached detail endpoint to include it.
      if (copyJson) copyJson.addEventListener('click', async function () {
        var original = copyJson.innerHTML;
        copyJson.disabled = true;
        copyJson.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
        var entry = await RM.resolveModEntry(id, name);
        copyJson.disabled = false;
        copyJson.innerHTML = original;
        RM.copyText(RM.modSnippet(entry), copyJson);
      });
      if (addBtn) addBtn.addEventListener('click', async function () {
        var original = addBtn.innerHTML;
        addBtn.disabled = true;
        addBtn.innerHTML = '<span class="spinner-border spinner-border-sm"></span>';
        var entry = await RM.resolveModEntry(id, name);
        var res = RM.addModToConfig(entry);
        addBtn.innerHTML = res.added ? '<i class="bi bi-check-lg"></i> Added' : 'Already in config';
        setTimeout(function () { addBtn.innerHTML = original; addBtn.disabled = false; }, 1600);
      });
    });
  }

  function renderPagination(meta) {
    var current = meta.currentPage || state.page;
    var total = meta.totalPages || 1;
    if (total <= 1) {
      paginationEl.innerHTML = '';
      return;
    }
    var html = '';
    html += '<button type="button" class="btn btn-outline-secondary btn-sm" id="mb-prev"' + (current <= 1 ? ' disabled' : '') + '><i class="bi bi-chevron-left"></i> Previous</button>';
    html += '<span class="page-info">Page</span>' +
      '<input type="number" class="form-control form-control-sm page-jump" id="mb-page-jump" min="1" max="' + total + '" value="' + current + '" aria-label="Go to page">' +
      '<span class="page-info">of ' + RM.esc(total.toLocaleString()) + '</span>';
    html += '<button type="button" class="btn btn-outline-secondary btn-sm" id="mb-next"' + (current >= total ? ' disabled' : '') + '>Next <i class="bi bi-chevron-right"></i></button>';
    paginationEl.innerHTML = html;
    var prev = document.getElementById('mb-prev');
    var next = document.getElementById('mb-next');
    var jump = document.getElementById('mb-page-jump');
    if (prev) prev.addEventListener('click', function () { goToPage(current - 1); });
    if (next) next.addEventListener('click', function () { goToPage(current + 1); });
    if (jump) jump.addEventListener('change', function () {
      var page = parseInt(jump.value, 10);
      if (page >= 1 && page <= total && page !== current) {
        goToPage(page);
      } else {
        jump.value = current;
      }
    });
  }

  function goToPage(page) {
    state.page = Math.max(1, page);
    pushState();
    load();
    window.scrollTo({ top: 0, behavior: 'smooth' });
  }

  function applySearch() {
    var newSearch = searchInput.value.trim().slice(0, 120);
    var newSort = sortSelect.value;
    if (newSearch === state.search && newSort === state.sort) return;
    state.search = newSearch;
    state.sort = newSort;
    state.page = 1;
    pushState();
    load();
  }

  form.addEventListener('submit', function (event) {
    event.preventDefault();
    applySearch();
  });
  searchInput.addEventListener('input', RM.debounce(applySearch, 400));
  categoryButtons.forEach(function (btn) {
    btn.addEventListener('click', function () {
      var next = btn.getAttribute('data-category');
      if (next === state.category) return;
      state.category = next;
      state.page = 1;
      syncCategoryButtons();
      pushState();
      load();
    });
  });
  sortSelect.addEventListener('change', applySearch);
  document.querySelectorAll('.mb-view-toggle [data-view]').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var next = btn.getAttribute('data-view');
      if (next === state.view) return;
      state.view = next;
      try { localStorage.setItem('rm.modBrowser.view', next); } catch (e) {}
      applyViewClass();
      pushState();
    });
  });

  window.addEventListener('popstate', function () {
    state = readStateFromURL();
    searchInput.value = state.search;
    syncCategoryButtons();
    sortSelect.value = state.sort;
    applyViewClass();
    load();
  });

  // Initial render: sync controls with the (normalized) URL and load.
  searchInput.value = state.search;
  syncCategoryButtons();
  sortSelect.value = state.sort;
  applyViewClass();
  pushState(true);
  load();
})();
