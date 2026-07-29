// eitri-settings — Settings page interactivity.
// Handles save-only Settings drafts, provider-aware Base URL show/hide,
// model refresh spinner, Test Connection, and dirty navigation guards.

(function () {
  'use strict';

  var settingsBaseline = '';
  var settingsDirty = false;
  var globalHandlersInstalled = false;

  function settingsForm() {
    return document.querySelector('#settings-form form');
  }

  function serializeSettingsForm(form) {
    if (!form) return '';
    var entries = [];
    var data = new FormData(form);
    data.forEach(function (value, key) {
      entries.push([key, String(value)]);
    });
    entries.sort(function (a, b) {
      if (a[0] === b[0]) return a[1] < b[1] ? -1 : a[1] > b[1] ? 1 : 0;
      return a[0] < b[0] ? -1 : 1;
    });
    return JSON.stringify(entries);
  }

  function updateDirtyState() {
    var form = settingsForm();
    if (!form) return;
    settingsDirty = serializeSettingsForm(form) !== settingsBaseline;

    var saveBtn = document.getElementById('settings-save-btn') || form.querySelector('button[type=submit]');
    if (saveBtn && !saveBtn.dataset.saving) {
      saveBtn.disabled = !settingsDirty;
      saveBtn.textContent = 'Save';
    }

    var revertBtn = document.getElementById('settings-revert-btn');
    if (revertBtn) revertBtn.disabled = !settingsDirty;

    var indicator = document.getElementById('settings-dirty-indicator');
    if (indicator) indicator.hidden = !settingsDirty;
  }

  function markCurrentFormClean() {
    var form = settingsForm();
    settingsBaseline = serializeSettingsForm(form);
    settingsDirty = false;
    updateDirtyState();
  }

  function installDirtyTracking() {
    var form = settingsForm();
    if (!form || form.dataset.dirtyTrackingInstalled === 'true') return;
    form.dataset.dirtyTrackingInstalled = 'true';
    markCurrentFormClean();
    form.addEventListener('input', updateDirtyState);
    form.addEventListener('change', updateDirtyState);
  }

  // — Provider-aware Base URL show/hide —
  function updateBaseURLVisibility() {
    var provider = document.getElementById('provider');
    var baseUrlGroup = document.querySelector('.base-url-group');
    if (!provider || !baseUrlGroup) return;

    var isCustomOpenAI = provider.value === 'custom_openai';
    baseUrlGroup.classList.toggle('base-url-hidden', !isCustomOpenAI);

    var baseUrlInput = document.getElementById('base_url');
    if (baseUrlInput) {
      baseUrlInput.required = isCustomOpenAI;
    }
  }

  function resetBaseURLToDefault() {
    var provider = document.getElementById('provider');
    if (!provider) return;
    var baseUrlInput = document.getElementById('base_url');
    if (!baseUrlInput) return;
    var defaults = {
      'opencode_go': 'https://opencode.ai/zen/go/v1',
      'github_copilot': 'https://api.githubcopilot.com',
      'custom_openai': '',
    };
    if (defaults[provider.value] !== undefined) {
      baseUrlInput.value = defaults[provider.value];
      baseUrlInput.dispatchEvent(new Event('input', { bubbles: true }));
    }
  }

  function initBaseURLToggle() {
    var provider = document.getElementById('provider');
    if (!provider || provider.dataset.baseUrlToggleInstalled === 'true') return;
    provider.dataset.baseUrlToggleInstalled = 'true';
    provider.addEventListener('change', function () {
      updateBaseURLVisibility();
      resetBaseURLToDefault();
      refreshModels();
      updateDirtyState();
    });
    updateBaseURLVisibility();
  }

  // — Model refresh spinner and fetch —
  function refreshModels() {
    var spinner = document.getElementById('model-refresh-spinner');
    if (spinner) spinner.style.visibility = 'visible';

    var form = settingsForm();
    var params = form ? new URLSearchParams(new FormData(form)) : new URLSearchParams();
    var url = '/api/models';
    var query = params.toString();
    if (query) url += '?' + query;

    fetch(url)
      .then(function (res) {
        if (!res.ok) throw new Error('Failed to fetch models');
        return res.json();
      })
      .then(function (data) {
        if (data.data && Array.isArray(data.data)) {
          updateModelSelect(data.data);
          updateDirtyState();
          if (data.data.length > 0) {
            showToast('\u2713 Models refreshed');
          } else {
            showToast('\u26a0 No models discovered. Check credentials and save.');
          }
        }
      })
      .catch(function (err) {
        showToast('Failed to refresh models: ' + err.message);
      })
      .finally(function () {
        if (spinner) spinner.style.visibility = 'hidden';
      });
  }

  function updateModelSelect(models) {
    var select = document.getElementById('model');
    if (!select) return;

    var currentValue = select.value;
    while (select.options.length > 0) {
      select.remove(0);
    }

    var placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.disabled = true;
    placeholder.selected = currentValue === '';
    placeholder.textContent = 'Select a model...';
    select.appendChild(placeholder);

    for (var i = 0; i < models.length; i++) {
      var opt = document.createElement('option');
      opt.value = models[i];
      opt.textContent = models[i];
      if (models[i] === currentValue) opt.selected = true;
      select.appendChild(opt);
    }
  }

  function showToast(message) {
    var notice = document.getElementById('model-refresh-notice');
    if (!notice) {
      notice = document.createElement('div');
      notice.id = 'model-refresh-notice';
      notice.className = 'model-refresh-notice';
      var modelGroup = document.querySelector('.form-group:has(#model)');
      if (modelGroup) {
        modelGroup.appendChild(notice);
      } else {
        var form = document.getElementById('settings-form');
        if (form) form.appendChild(notice);
      }
    }
    notice.textContent = message;
    notice.classList.remove('fade-out');

    if (notice._hideTimer) clearTimeout(notice._hideTimer);

    notice._hideTimer = setTimeout(function () {
      notice.classList.add('fade-out');
      setTimeout(function () {
        notice.textContent = '';
      }, 600);
    }, 2500);
  }

  // — Save success fade-out —
  function initSaveSuccessFade() {
    var badge = document.querySelector('.save-success');
    if (!badge) return;
    if (badge._fadeTimer) clearTimeout(badge._fadeTimer);
    badge._fadeTimer = setTimeout(function () {
      badge.classList.add('fade-out');
    }, 2500);
  }

  // — Error auto-scroll —
  function scrollToErrorIfPresent() {
    var toast = document.querySelector('.error-toast');
    if (toast) {
      toast.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
  }

  // — Revert to saved config without writing —
  function initRevertButton() {
    var btn = document.getElementById('settings-revert-btn');
    if (!btn || btn.dataset.revertInstalled === 'true') return;
    btn.dataset.revertInstalled = 'true';
    btn.addEventListener('click', function () {
      if (btn.disabled) return;
      btn.disabled = true;
      fetch('/api/config', { headers: { 'HX-Request': 'true' } })
        .then(function (res) {
          if (!res.ok) throw new Error('Failed to revert settings');
          return res.text();
        })
        .then(function (html) {
          var current = document.getElementById('settings-form');
          if (!current) return;
          var wrapper = document.createElement('div');
          wrapper.innerHTML = html;
          var replacement = wrapper.querySelector('#settings-form');
          if (replacement) {
            current.replaceWith(replacement);
            init();
          }
        })
        .catch(function (err) {
          showToast(err.message);
          updateDirtyState();
        });
    });
  }

  // — Ctrl+Enter to submit form —
  function initCtrlEnter() {
    var form = settingsForm();
    if (!form || form.dataset.ctrlEnterInstalled === 'true') return;
    form.dataset.ctrlEnterInstalled = 'true';

    function handleKeydown(e) {
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
        e.preventDefault();
        var submitBtn = form.querySelector('button[type=submit]');
        if (submitBtn && !submitBtn.disabled) {
          submitBtn.click();
        }
      }
    }

    var inputs = form.querySelectorAll('input, textarea, select');
    for (var i = 0; i < inputs.length; i++) {
      inputs[i].addEventListener('keydown', handleKeydown);
    }
  }

  function wantsSettingsNavigationGuard(target) {
    if (!settingsDirty) return false;
    if (!target) return false;
    if (target.closest('#settings-form')) return false;
    var link = target.closest('a[href]');
    if (!link) return false;
    var href = link.getAttribute('href') || '';
    if (href === '' || href.charAt(0) === '#') return false;
    return true;
  }

  function confirmDiscardDraft() {
    return window.confirm('Discard unsaved Settings changes?');
  }

  function installGlobalHandlers() {
    if (globalHandlersInstalled) return;
    globalHandlersInstalled = true;

    document.body.addEventListener('htmx:beforeSend', function (evt) {
      var target = evt.detail && evt.detail.target;
      if (target && target.id === 'settings-form') {
        var btn = document.getElementById('settings-save-btn') || document.querySelector('#settings-form button[type=submit]');
        if (btn) {
          btn.disabled = true;
          btn.dataset.saving = 'true';
          btn._origText = btn.textContent;
          btn.textContent = 'Saving…';
        }
        var revertBtn = document.getElementById('settings-revert-btn');
        if (revertBtn) revertBtn.disabled = true;
      }

      if (target && target.id === 'test-connection-result') {
        var testBtn = document.getElementById('test-connection-btn');
        if (testBtn) testBtn.disabled = true;
        var result = document.getElementById('test-connection-result');
        if (result) result.innerHTML = '<span class="test-connection-pending">Testing...</span>';
      }
    });

    document.body.addEventListener('htmx:afterSwap', function (evt) {
      var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
      if (targetId === 'settings-form') {
        var btn = document.getElementById('settings-save-btn') || document.querySelector('#settings-form button[type=submit]');
        if (btn) {
          btn.textContent = btn._origText || 'Save';
          delete btn._origText;
          delete btn.dataset.saving;
        }
        init();
      }
      if (targetId === 'test-connection-result') {
        var testBtn = document.getElementById('test-connection-btn');
        if (testBtn) testBtn.disabled = false;
      }
      if (targetId === 'app' && document.getElementById('settings-form')) {
        init();
      }
    });

    document.body.addEventListener('htmx:afterOnLoad', function (evt) {
      var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
      if (targetId === 'settings-form') {
        var spinner = document.getElementById('model-refresh-spinner');
        if (spinner) spinner.style.visibility = 'hidden';
      }
    });

    document.addEventListener('click', function (evt) {
      if (!wantsSettingsNavigationGuard(evt.target)) return;
      if (!confirmDiscardDraft()) {
        evt.preventDefault();
        evt.stopPropagation();
        evt.stopImmediatePropagation();
      }
    }, true);

    document.body.addEventListener('htmx:beforeRequest', function (evt) {
      if (!settingsDirty) return;
      var elt = evt.detail && evt.detail.elt;
      if (!elt || elt.closest('#settings-form')) return;
      if (elt.matches && elt.matches('a[href]')) {
        if (!confirmDiscardDraft()) evt.preventDefault();
      }
    });

    window.addEventListener('beforeunload', function (evt) {
      if (!settingsDirty) return;
      evt.preventDefault();
      evt.returnValue = '';
    });
  }

  function init() {
    installGlobalHandlers();
    if (!document.getElementById('settings-form')) return;
    initBaseURLToggle();
    installDirtyTracking();
    initSaveSuccessFade();
    scrollToErrorIfPresent();
    initRevertButton();
    initCtrlEnter();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
