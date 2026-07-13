// eitri-mermaid — Browser island for idempotent Mermaid diagram initialization.
// Runs on page load and after HTMX swaps. Tolerates missing Mermaid.js.
(function () {
  'use strict';

  function initMermaid() {
    if (typeof mermaid === 'undefined') return;

    mermaid.initialize({
      startOnLoad: false,
      theme: 'dark',
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
})();
