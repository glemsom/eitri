// eitri-settings — Settings page interactivity.
// Handles provider-aware Base URL show/hide, model refresh spinner,
// and Test Connection button behavior.

(function () {
  'use strict';

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
      // Set default base URL based on provider
      var defaults = {
        'opencode_go': 'https://opencode.ai/zen/go',
        'github_copilot': 'https://api.githubcopilot.com',
        'custom_openai': '',
      };
      // Reset to default when switching provider
      if (defaults[provider.value] !== undefined) {
        baseUrlInput.value = defaults[provider.value];
      }
    }
  }

  function initBaseURLToggle() {
    var provider = document.getElementById('provider');
    if (!provider) return;
    provider.addEventListener('change', function () {
      updateBaseURLVisibility();
      // Fetch models via API to refresh model dropdown
      refreshModels();
    });
    // Set initial state
    updateBaseURLVisibility();
  }

  // — Model refresh spinner and fetch —
  function refreshModels() {
    var spinner = document.getElementById('model-refresh-spinner');
    if (spinner) spinner.style.display = 'inline-block';

    // Fetch models via fetch API (not HTMX) to get JSON, then update select
    fetch('/api/models')
      .then(function (res) {
        if (!res.ok) throw new Error('Failed to fetch models');
        return res.json();
      })
      .then(function (data) {
        if (data.data && Array.isArray(data.data)) {
          updateModelSelect(data.data);
          showToast('✓ Models refreshed');
        }
      })
      .catch(function () {
        showToast('Model refresh failed');
      })
      .finally(function () {
        if (spinner) spinner.style.display = 'none';
      });
  }

  function updateModelSelect(models) {
    var select = document.getElementById('model');
    if (!select) return;

    var currentValue = select.value;

    // Clear options
    while (select.options.length > 0) {
      select.remove(0);
    }

    // Add placeholder
    var placeholder = document.createElement('option');
    placeholder.value = '';
    placeholder.disabled = true;
    placeholder.selected = currentValue === '';
    placeholder.textContent = 'Select a model...';
    select.appendChild(placeholder);

    // Add model options
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
    notice.innerHTML = message;
    notice.classList.remove('fade-out');

    // Clear any pending hide timer
    if (notice._hideTimer) clearTimeout(notice._hideTimer);

    notice._hideTimer = setTimeout(function () {
      notice.classList.add('fade-out');
      setTimeout(function () {
        notice.innerHTML = '';
      }, 600);
    }, 2500);
  }

  // — Test Connection button —
  function initTestConnection() {
    var btn = document.getElementById('test-connection-btn');
    if (!btn) return;

    // Disable button while request is in flight
    document.body.addEventListener('htmx:beforeSend', function (evt) {
      var target = evt.detail && evt.detail.target;
      if (target && target.id === 'test-connection-result') {
        var btn2 = document.getElementById('test-connection-btn');
        if (btn2) btn2.disabled = true;
        var result = document.getElementById('test-connection-result');
        if (result) result.innerHTML = '<span class="test-connection-pending">Testing...</span>';
      }
    });

    // Re-enable button after swap (response arrives)
    document.body.addEventListener('htmx:afterSwap', function (evt) {
      var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
      if (targetId === 'test-connection-result') {
        var btn2 = document.getElementById('test-connection-btn');
        if (btn2) btn2.disabled = false;
      }
    });

    // Handle model refresh spinner when settings form is re-rendered
    // (after PUT /api/config via form save)
    document.body.addEventListener('htmx:afterOnLoad', function (evt) {
      var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
      if (targetId === 'settings-form') {
        var spinner = document.getElementById('model-refresh-spinner');
        if (spinner) spinner.style.display = 'none';
      }
    });
  }

  // — Init on page load —
  function init() {
    if (!document.getElementById('settings-form')) return;
    initBaseURLToggle();
    initTestConnection();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // Re-init after HTMX swaps (form re-render)
  document.addEventListener('htmx:afterSwap', function (evt) {
    var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
    if (targetId === 'settings-form') {
      initBaseURLToggle();
    }
  });

})();
