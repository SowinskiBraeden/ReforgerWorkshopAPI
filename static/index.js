var u = new URL(window.location.href);
var d = u.searchParams.get('page');
var defaultPage = document.body ? document.body.getAttribute('data-default-page') : '';
var a = '';
var navKey = d || document.body?.getAttribute('data-nav-key') || defaultPage || 'home';
if (navKey.indexOf('guide-') === 0) navKey = 'guides';
if (navKey === 'api' || navKey === 'mod-structures' || navKey === 'changelog' || navKey === 'methodology' || navKey === 'documentation/api' || navKey === 'documentation/mods' || navKey === 'documentation/changelog') navKey = 'docs';
var siteUrl = (document.body && document.body.getAttribute('data-site-url')) || 'https://reforgermods.net/';
if (siteUrl.charAt(siteUrl.length - 1) !== '/') {
  siteUrl += '/';
}
var pageMeta = {
  home: {
    title: 'Arma Reforger Mods API & Workshop Data | Reforger Mods API',
    description: 'Reforger Mods API provides searchable Arma Reforger Workshop mod metadata, normalized JSON responses, mod details, dependencies, scenarios, and cache-friendly API endpoints.',
    canonical: siteUrl
  },
  'documentation/api': {
    title: 'Arma Reforger API Documentation | Reforger Mods API',
    description: 'API documentation for searching Arma Reforger Workshop mods and fetching normalized mod metadata, dependencies, scenarios, cache headers, and rate limits.',
    canonical: siteUrl + '?page=documentation/api'
  },
  'documentation/mods': {
    title: 'Arma Reforger Mod Data Structures | Reforger Mods API',
    description: 'Reference for Arma Reforger Workshop mod preview, detail, dependency, and scenario JSON structures returned by Reforger Mods API.',
    canonical: siteUrl + '?page=documentation/mods'
  },
  'documentation/changelog': {
    title: 'Reforger Mods API Changelog | Arma Reforger Workshop API',
    description: 'Version history and release notes for Reforger Mods API, a public API for Arma Reforger Workshop mod metadata.',
    canonical: siteUrl + '?page=documentation/changelog'
  },
  privacy: {
    title: 'Privacy Policy | Reforger Mods API',
    description: 'Privacy policy for Reforger Mods API, an independent Arma Reforger Workshop metadata API.',
    canonical: siteUrl + '?page=privacy'
  },
  terms: {
    title: 'Terms of Service | Reforger Mods API',
    description: 'Terms of service for Reforger Mods API, an independent Arma Reforger Workshop metadata API.',
    canonical: siteUrl + '?page=terms'
  }
};
var meta = pageMeta[navKey] || pageMeta.home;
if (d) {
  document.title = meta.title;
  setMeta('description', meta.description);
  setMeta('og:title', meta.title, true);
  setMeta('og:description', meta.description, true);
  setMeta('og:url', meta.canonical, true);
  setMeta('twitter:title', meta.title);
  setMeta('twitter:description', meta.description);
  setCanonical(meta.canonical);
}

document.querySelectorAll('[data-nav-page]').forEach(function(link) {
  if (link.getAttribute('data-nav-page') === navKey) {
    link.classList.add('active');
    // Highlight the parent dropdown toggle when a dropdown item is active.
    var dropdown = link.closest('.dropdown');
    var toggle = dropdown && dropdown.querySelector('[data-nav-group]');
    if (toggle) toggle.classList.add('active');
  }
});
try { document.querySelector('a[href="?page='+d+'"] button').classList.add('docs-nav-active'); } catch {}
if(d) { a = '/static/pages/'+d+'.md' } else if (defaultPage) { a = '/static/pages/'+defaultPage+'.md' }
if (!a) {
  document.querySelectorAll('#content pre code').forEach(function(code) {
    hljs.highlightElement(code);
  });
} else {
fetch(a)
  .then(b => {
    if (!b.ok) {
      throw new Error(`Network response was not ok: ${b.status}`);
      window.location="?page=Error"
    }
    return b.text();
  })
  .then(markdownContent => {
    document.getElementById('content').innerHTML = marked.parse(markdownContent);
    document.querySelectorAll(".hl-escape").forEach(function(element) {
      element.innerHTML = element.innerHTML.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#039;");
    });
    document.querySelectorAll('#content pre code').forEach(function(code) {
      hljs.highlightElement(code);
    });
  })
  .catch(c => {
    console.error('Error fetching the Markdown content:', c);
    if(window.location.hostname != "127.0.0.1") {
      window.location="?page=Error"
    }
  });
}

function setMeta(name, content, property) {
  var selector = property ? 'meta[property="' + name + '"]' : 'meta[name="' + name + '"]';
  var element = document.querySelector(selector);
  if (element) {
    element.setAttribute('content', content);
  }
}

function setCanonical(href) {
  var link = document.querySelector('link[rel="canonical"]');
  if (link) {
    link.setAttribute('href', href);
  }
}
