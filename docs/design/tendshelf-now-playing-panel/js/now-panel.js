/* Tendshelf now playing panel: the few things the server can't do. No dependencies.
   - Header shows the small cover and title once the big cover has scrolled away.
   - "Read more" expands the summary.
   - Tabs: selected state and keyboard (HTMX loads the tab panels).
   - Chapters tab: scroll the current chapter into view when it loads.
   Call once; it uses event delegation, so HTMX swaps inside the panel need no re-binding. */
(function () {
  'use strict';
  function init(panel) {
    if (!panel || panel.dataset.npReady) return;
    panel.dataset.npReady = '1';
    var scroll = panel.querySelector('[data-now-panel-scroll]');
    var hero = panel.querySelector('[data-now-panel-hero]');
    if (scroll && hero && 'IntersectionObserver' in window) {
      new IntersectionObserver(function (e) {
        panel.classList.toggle('is-scrolled', !e[0].isIntersecting);
      }, { root: scroll, threshold: 0, rootMargin: '-56px 0px 0px 0px' }).observe(hero.querySelector('.ts-cover') || hero);
    }
    panel.addEventListener('click', function (e) {
      var more = e.target.closest('[data-now-panel-more]');
      if (more) {
        var text = document.getElementById(more.getAttribute('aria-controls'));
        var open = more.getAttribute('aria-expanded') !== 'true';
        text.classList.toggle('is-open', open);
        more.setAttribute('aria-expanded', String(open));
        more.textContent = open ? 'Show less' : 'Read more';
      }
      var tab = e.target.closest('[role="tab"]');
      if (tab) {
        tab.parentElement.querySelectorAll('[role="tab"]').forEach(function (t) { t.setAttribute('aria-selected', String(t === tab)); });
        var body = panel.querySelector('[role="tabpanel"]');
        if (body) body.setAttribute('aria-label', tab.textContent.split('·')[0].trim());
      }
    });
    panel.addEventListener('keydown', function (e) {
      var tab = e.target.closest('[role="tab"]');
      if (!tab || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight')) return;
      var tabs = Array.prototype.slice.call(tab.parentElement.querySelectorAll('[role="tab"]'));
      var next = tabs[(tabs.indexOf(tab) + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length];
      next.focus(); next.click(); e.preventDefault();
    });
    document.body.addEventListener('htmx:afterSwap', function (e) {
      if (!panel.contains(e.target)) return;
      var cur = e.target.querySelector('[aria-current="true"]');
      if (cur) cur.scrollIntoView({ block: 'center', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
    });
  }
  window.TendshelfNowPanel = { init: init };
  document.addEventListener('DOMContentLoaded', function () { init(document.getElementById('now-panel')); });
})();
