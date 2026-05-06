/* ========================================
   ezstack — Back to Top
   ======================================== */

/* A small floating button that appears after the user has scrolled past
   ~800px and scrolls smoothly back to the top of the page. Self-contained
   so it can drop onto any docs page without coordination with that page's
   inline scripts. */

(function () {
  'use strict';

  var SHOW_AFTER_PX = 800;

  function build() {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'back-to-top';
    btn.id = 'back-to-top';
    btn.setAttribute('aria-label', 'Back to top');
    btn.title = 'Back to top';
    btn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 15l-6-6-6 6"/></svg>';
    return btn;
  }

  function init() {
    if (document.getElementById('back-to-top')) return; // already wired
    var btn = build();
    document.body.appendChild(btn);

    var ticking = false;
    function onScroll() {
      if (ticking) return;
      ticking = true;
      requestAnimationFrame(function () {
        if (window.scrollY > SHOW_AFTER_PX) {
          btn.classList.add('visible');
        } else {
          btn.classList.remove('visible');
        }
        ticking = false;
      });
    }
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });

    btn.addEventListener('click', function () {
      // History gets a clean URL (no stray hash) when the user resets scroll.
      if (window.location.hash) {
        history.replaceState(null, '', window.location.pathname + window.location.search);
      }
      window.scrollTo({ top: 0, behavior: 'smooth' });
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
