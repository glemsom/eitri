// eitri-persona-selector — Browser island for the persona selector dropdown.

(function () {
  'use strict';

  function init() {
    document.querySelectorAll('#persona-selector').forEach(function (el) {
      if (el.dataset.psInitialized) return;
      el.dataset.psInitialized = 'true';

      var trigger = el.querySelector('[data-ps-target="trigger"]');
      var dropdown = el.querySelector('[data-ps-target="dropdown"]');
      var chevron = el.querySelector('[data-ps-target="chevron"]');
      if (!trigger || !dropdown) return;

      function open() {
        dropdown.hidden = false;
        trigger.setAttribute('aria-expanded', 'true');
        if (chevron) chevron.classList.add('persona-chevron-open');
      }

      function close() {
        dropdown.hidden = true;
        trigger.setAttribute('aria-expanded', 'false');
        if (chevron) chevron.classList.remove('persona-chevron-open');
      }

      function toggle() {
        if (dropdown.hidden) {
          open();
        } else {
          close();
        }
      }

      // Click on trigger toggles dropdown
      trigger.addEventListener('click', function (e) {
        e.stopPropagation();
        toggle();
      });

      // Click outside the selector closes the dropdown
      document.addEventListener('click', function (e) {
        if (!el.contains(e.target)) {
          close();
        }
      });

      // Keyboard: Enter/Space toggles dropdown on trigger
      trigger.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          toggle();
        }
      });

      // Keyboard: Escape closes dropdown and returns focus to trigger
      el.addEventListener('keydown', function (e) {
        if (e.key === 'Escape' && !dropdown.hidden) {
          close();
          trigger.focus();
        }
      });
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  document.addEventListener('htmx:afterSwap', function () {
    init();
  });
})();
