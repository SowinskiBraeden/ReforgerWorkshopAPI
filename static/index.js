var navKey = document.body?.getAttribute('data-nav-key') || 'home';
if (navKey.indexOf('guide-') === 0) navKey = 'guides';
if (navKey === 'api-quickstart') navKey = 'api';
if (navKey === 'mod-structures') navKey = 'docs';

document.querySelectorAll('[data-nav-page]').forEach(function(link) {
  if (link.getAttribute('data-nav-page') === navKey) {
    link.classList.add('active');
    var dropdown = link.closest('.dropdown');
    var toggle = dropdown && dropdown.querySelector('[data-nav-group]');
    if (toggle) toggle.classList.add('active');
  }
});

document.querySelectorAll('#content pre code').forEach(function(code) {
  hljs.highlightElement(code);
});

document.querySelectorAll('[data-api-code-tabs]').forEach(function (tabs) {
  tabs.addEventListener('click', function (event) {
    var button = event.target.closest('[data-api-code-tab]');
    if (!button) return;
    var target = button.getAttribute('data-api-code-tab');
    tabs.querySelectorAll('[data-api-code-tab]').forEach(function (tab) {
      tab.classList.toggle('active', tab === button);
    });
    tabs.querySelectorAll('[data-api-code-panel]').forEach(function (panel) {
      panel.hidden = panel.getAttribute('data-api-code-panel') !== target;
    });
  });
});

var heroSlides = document.querySelectorAll('.home-hero-slide');
if (heroSlides.length > 1 && !window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
  var heroSlideIndex = 0;
  setInterval(function () {
    heroSlides[heroSlideIndex].classList.remove('is-active');
    heroSlideIndex = (heroSlideIndex + 1) % heroSlides.length;
    heroSlides[heroSlideIndex].classList.add('is-active');
  }, 5000);
}
