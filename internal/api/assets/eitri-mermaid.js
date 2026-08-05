// eitri-mermaid — Browser island for idempotent Mermaid diagram initialization.
// Runs on page load and after HTMX swaps. Tolerates missing Mermaid.js.
(function () {
  'use strict';

  function initMermaid() {
    if (typeof mermaid === 'undefined') return;

    // Detect color scheme for theme selection (issue #977)
    var isLightTheme = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches;
    var theme = isLightTheme ? 'default' : 'dark';

    mermaid.initialize({
      startOnLoad: false,
      theme: theme,
      securityLevel: 'loose',
    });

    document.querySelectorAll('pre.mermaid:not([data-mermaid-processed])').forEach(function (el) {
      el.setAttribute('data-mermaid-processed', 'true');
      try {
        mermaid.run({ nodes: [el] });
      } catch (e) {
        console.warn('Mermaid render failed:', e);
        // Show raw code as fallback
        el.classList.add('mermaid-error');
        el.insertAdjacentHTML('afterend', '<p class="text-muted">Diagram render failed. Raw code:</p>');
      }
    });
  }

  // Run on load
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () {
      // Give Mermaid.js time to load
      setTimeout(initMermaid, 100);
    });
  } else {
    setTimeout(initMermaid, 100);
  }

  // Run after HTMX swaps
  document.addEventListener('htmx:afterSwap', function () {
    setTimeout(initMermaid, 100);
  });
  document.addEventListener('htmx:afterSettle', function () {
    setTimeout(initMermaid, 50);
  });

  // Run once the lazy loader has fetched mermaid.min.js on demand
  // (issue #968). On a page with no diagrams the library never loads, so this
  // event never fires and initMermaid simply returns early.
  document.addEventListener('eitri:mermaid-loaded', function () {
    setTimeout(initMermaid, 100);
  });

  // The lazy loader reports a failed fetch of mermaid.min.js (issue #1078).
  // Degrade every untouched diagram to its raw source with a visible message
  // instead of leaving a silently unrendered block.
  document.addEventListener('eitri:mermaid-load-failed', function () {
    document.querySelectorAll('pre.mermaid:not([data-mermaid-processed])').forEach(function (el) {
      el.setAttribute('data-mermaid-processed', 'true');
      el.classList.add('mermaid-error');
      el.insertAdjacentHTML('afterend', '<p class="text-muted">Diagram renderer could not be loaded. Raw code:</p>');
    });
  });
})();
