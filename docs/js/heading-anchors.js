/* ========================================
   ezstack — Shareable Heading Anchors
   ======================================== */

/* Adds a clickable "#" link next to every content heading on every docs page
   so any heading can be permalinked. Headings inside <nav>, <footer>, hero
   sections, terminal demos, and other chrome are intentionally skipped. */

(function () {
  'use strict';

  // Selectors for elements whose descendant headings should NOT be made
  // shareable. These are page chrome and decorative containers.
  var EXCLUDE_SELECTORS = [
    'nav',
    'footer',
    'header',
    '.nav',
    '.footer',
    '.terminal',
    '.terminal-section',
    '.docs-search',
    '.docs-search-dropdown',
    '.docs-sidebar',
    '.hero',
    '.agent-hero',
    '.feature-card-icon',
    '.copy-btn',
  ];

  function isInsideExcluded(el) {
    for (var i = 0; i < EXCLUDE_SELECTORS.length; i++) {
      if (el.closest(EXCLUDE_SELECTORS[i])) return true;
    }
    return false;
  }

  // Slugify text content the same way GitHub / Goldmark autoheading IDs do:
  // lowercase, strip everything that isn't alnum/space/hyphen, collapse
  // whitespace to hyphens. We never overwrite an existing id, so the slugs
  // generated here are only used for headings without a server-side id.
  function slugify(text) {
    return String(text || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9\s-]/g, '')
      .replace(/\s+/g, '-')
      .replace(/-+/g, '-')
      .replace(/^-|-$/g, '');
  }

  function ensureUniqueId(base) {
    if (!base) return '';
    if (!document.getElementById(base)) return base;
    var i = 2;
    while (document.getElementById(base + '-' + i)) i++;
    return base + '-' + i;
  }

  function buildAnchor(targetId) {
    var a = document.createElement('a');
    a.className = 'heading-anchor';
    a.href = '#' + targetId;
    a.setAttribute('aria-label', 'Copy link to this section');
    a.title = 'Copy link to this section';
    a.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>';
    return a;
  }

  function addToast() {
    var toast = document.getElementById('heading-anchor-toast');
    if (!toast) {
      toast = document.createElement('div');
      toast.id = 'heading-anchor-toast';
      toast.className = 'heading-anchor-toast';
      toast.textContent = 'Link copied';
      document.body.appendChild(toast);
    }
    toast.classList.remove('visible');
    // force reflow so the next add re-triggers the transition
    void toast.offsetWidth;
    toast.classList.add('visible');
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { toast.classList.remove('visible'); }, 1600);
  }

  function copyShareLink(headingId) {
    // Build the canonical absolute URL for this page + heading. We strip any
    // existing hash/query so the share link is clean.
    var base = window.location.origin + window.location.pathname;
    var url = base + '#' + headingId;
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(url).then(addToast, function () {
        // Fallback: just update the URL bar so the user can copy manually.
        history.replaceState(null, '', '#' + headingId);
      });
    } else {
      history.replaceState(null, '', '#' + headingId);
      addToast();
    }
  }

  function decorate(heading) {
    if (heading.dataset.anchored === '1') return;
    if (isInsideExcluded(heading)) return;
    if (!heading.textContent || !heading.textContent.trim()) return;

    if (!heading.id) {
      var slug = ensureUniqueId(slugify(heading.textContent));
      if (!slug) return;
      heading.id = slug;
    }

    heading.classList.add('has-heading-anchor');

    var a = buildAnchor(heading.id);
    a.addEventListener('click', function (e) {
      e.preventDefault();
      // Update the URL hash and scroll into view (smooth where supported).
      history.replaceState(null, '', '#' + heading.id);
      heading.scrollIntoView({ behavior: 'smooth', block: 'start' });
      copyShareLink(heading.id);
    });
    heading.appendChild(document.createTextNode(' '));
    heading.appendChild(a);
    heading.dataset.anchored = '1';
  }

  function decorateAll() {
    var headings = document.querySelectorAll('h2, h3, h4');
    for (var i = 0; i < headings.length; i++) {
      decorate(headings[i]);
    }
  }

  // On initial load, if there's already a hash in the URL, scroll to it after
  // we've added our anchor markers (which can shift layout slightly).
  function focusFromHash() {
    var hash = window.location.hash;
    if (!hash || hash === '#') return;
    try {
      var target = document.querySelector(hash);
      if (target) {
        // Defer to next tick so layout settles after we mutated the headings.
        setTimeout(function () {
          target.scrollIntoView({ block: 'start' });
        }, 0);
      }
    } catch (_) { /* malformed hash like "#weird:char" — ignore */ }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      decorateAll();
      focusFromHash();
    });
  } else {
    decorateAll();
    focusFromHash();
  }
})();
