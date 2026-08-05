// eitri-persona-selector — Browser island for the persona selector dropdown.
//
// Keyboard model (issue #1074): the trigger is a button that opens a
// single-select listbox (WAI-ARIA listbox pattern with a roving tabindex).
// The trigger advertises the popup via aria-haspopup/aria-expanded/
// aria-controls and the options expose their selection state through
// aria-selected. While the dropdown is open, ArrowUp/ArrowDown/Home/End move
// focus between the options, Enter/Space activate the focused option (native
// button activation → htmx POST), Tab closes the widget, and Escape closes it
// and returns focus to the trigger.

(function () {
  'use strict';

  function getOptions(el) {
    return Array.prototype.slice.call(
      el.querySelectorAll('[data-ps-target="dropdown"] [role="option"]')
    );
  }

  // focusOption implements the listbox roving tabindex: the focused option is
  // the only one in the tab order, so Tab exits the widget instead of moving
  // through every option.
  function focusOption(options, index) {
    for (var i = 0; i < options.length; i++) {
      options[i].tabIndex = i === index ? 0 : -1;
    }
    options[index].focus();
  }

  // personaMoveFocus returns the option index to focus for the given key,
  // wrapping around the ends of the list; -1 means the key does not move
  // focus. Kept pure for unit-testing.
  function personaMoveFocus(key, currentIndex, optionCount) {
    if (optionCount <= 0) return -1;
    if (key === 'ArrowDown') return (currentIndex + 1) % optionCount;
    if (key === 'ArrowUp') return (currentIndex - 1 + optionCount) % optionCount;
    if (key === 'Home') return 0;
    if (key === 'End') return optionCount - 1;
    return -1;
  }

  function open(el) {
    var dropdown = el.querySelector('[data-ps-target="dropdown"]');
    var trigger = el.querySelector('[data-ps-target="trigger"]');
    var chevron = el.querySelector('[data-ps-target="chevron"]');
    dropdown.hidden = false;
    trigger.setAttribute('aria-expanded', 'true');
    if (chevron) chevron.classList.add('persona-chevron-open');

    // Move focus to the selected option (or the first one) so arrow keys and
    // Enter/Space work immediately after opening.
    var options = getOptions(el);
    if (options.length === 0) return;
    var selectedIndex = 0;
    for (var i = 0; i < options.length; i++) {
      if (options[i].getAttribute('aria-selected') === 'true') {
        selectedIndex = i;
        break;
      }
    }
    focusOption(options, selectedIndex);
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

      // Keyboard on trigger: Enter/Space toggles; ArrowDown/ArrowUp on the
      // closed trigger open the listbox so navigation starts immediately.
      trigger.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          toggle(el);
        } else if (dropdown.hidden && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
          e.preventDefault();
          open(el);
        }
      });

      // Keyboard: Escape closes the dropdown and returns focus to the trigger.
      el.addEventListener('keydown', function (e) {
        if (e.key === 'Escape' && !dropdown.hidden) {
          close(el);
          trigger.focus();
        }
      });

      // Arrow/Home/End navigation inside the open listbox. Options are native
      // buttons, so Enter/Space on the focused option activates it natively
      // (htmx POST). Tab closes the widget and re-focuses the trigger so the
      // browser continues past the widget instead of tabbing through options.
      dropdown.addEventListener('keydown', function (e) {
        var options = getOptions(el);
        var index = options.indexOf(document.activeElement);
        if (index === -1) return;
        var next = personaMoveFocus(e.key, index, options.length);
        if (next === -1) {
          if (e.key === 'Tab') {
            close(el);
            trigger.focus();
          }
          return;
        }
        e.preventDefault();
        focusOption(options, next);
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

  document.addEventListener('htmx:afterSwap', function (e) {
    init();
    // A persona activation swaps #persona-selector for a fresh element; hand
    // focus back to the new trigger so keyboard users can keep operating the
    // dropdown right after selecting (issue #1074).
    if (e.detail && e.detail.target && e.detail.target.id === 'persona-selector') {
      var fresh = document.getElementById('persona-selector');
      var trigger = fresh && fresh.querySelector('[data-ps-target="trigger"]');
      if (trigger && document.activeElement !== trigger) trigger.focus();
    }
  });
})();
