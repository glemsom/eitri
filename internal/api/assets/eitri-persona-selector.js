// eitri-persona-selector — Browser island for the persona selector dropdown.

(function () {
  'use strict';

  function open(el) {
    var dropdown = el.querySelector('[data-ps-target="dropdown"]');
    var trigger = el.querySelector('[data-ps-target="trigger"]');
    var chevron = el.querySelector('[data-ps-target="chevron"]');
    dropdown.hidden = false;
    trigger.setAttribute('aria-expanded', 'true');
    if (chevron) chevron.classList.add('persona-chevron-open');
  }

  function close(el) {
    var dropdown = el.querySelector('[data-ps-target="dropdown"]');
    var trigger = el.querySelector('[data-ps-target="trigger"]');
    var chevron = el.querySelector('[data-ps-target="chevron"]');
    dropdown.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
    if (chevron) chevron.classList.remove('persona-chevron-open');
  }

  function toggle(el) {
    var dropdown = el.querySelector('[data-ps-target="dropdown"]');
    if (dropdown.hidden) {
      open(el);
    } else {
      close(el);
    }
  }

  function init() {
    document.querySelectorAll('#persona-selector').forEach(function (el) {
      if (el.dataset.psInitialized) return;
      el.dataset.psInitialized = 'true';

      var trigger = el.querySelector('[data-ps-target="trigger"]');
      var dropdown = el.querySelector('[data-ps-target="dropdown"]');
      if (!trigger || !dropdown) return;

      // Click on trigger toggles dropdown. stopPropagation keeps the delegated
      // document listener below from immediately closing the freshly opened
      // dropdown.
      trigger.addEventListener('click', function (e) {
        e.stopPropagation();
        toggle(el);
      });

      // Keyboard: Enter/Space toggles dropdown on trigger
      trigger.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          toggle(el);
        }
      });

      // Keyboard: Escape closes dropdown and returns focus to trigger
      el.addEventListener('keydown', function (e) {
        if (e.key === 'Escape' && !dropdown.hidden) {
          close(el);
          trigger.focus();
        }
      });
    });
  }

  // One delegated document-level click listener, registered once at module
  // scope, closes any open persona dropdown when a click lands outside it.
  // It walks the current DOM on every click, so it never closes over a
  // detached element — htmx re-creating the selector cannot accumulate
  // permanent per-element listeners (issue #1069).
  document.addEventListener('click', function (e) {
    document.querySelectorAll('#persona-selector').forEach(function (el) {
      if (el.dataset.psInitialized !== 'true') return;
      if (el.contains(e.target)) return;
      var dropdown = el.querySelector('[data-ps-target="dropdown"]');
      if (dropdown && !dropdown.hidden) {
        close(el);
      }
    });
  });

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  document.addEventListener('htmx:afterSwap', function () {
    init();
  });
})();
