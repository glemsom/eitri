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
      // Only reset if input is hidden (switching away from custom)
      // or if empty
      if (!isCustomOpenAI || !baseUrlInput.value || baseUrlInput.value === defaults['opencode_go'] || baseUrlInput.value === defaults['github_copilot']) {
        if (defaults[provider.value] !== undefined) {
          baseUrlInput.value = defaults[provider.value];
        }
      }
    }
  }

  function initBaseURLToggle() {
    var provider = document.getElementById('provider');
    if (!provider) return;
    provider.addEventListener('change', function () {
      updateBaseURLVisibility();
      // Trigger model refresh by submitting the form's HTMX PUT
      // Actually, provider change initiates a model fetch via HTMX.
      // The form already has hx-put, but we need to trigger model refresh
      // without saving. We'll do an hx-get on the models endpoint.
      triggerModelRefresh();
    });
    // Set initial state
    updateBaseURLVisibility();
  }

  // — Model refresh spinner —
  function triggerModelRefresh() {
    var spinner = document.getElementById('model-refresh-spinner');
    if (spinner) spinner.style.display = 'inline-block';

    // Use HTMX to fetch models via PUT /api/config triggered by form submit
    // but we don't want to save. Instead, use GET /api/models via HTMX
    // and on success update the model select.
    htmx.ajax('GET', '/api/models', {
      source: document.getElementById('test-connection-btn') || document.body,
      target: '#model-refresh-notice',
      swap: 'innerHTML',
      handler: function (el, targetInfo) {
        // Hide spinner
        if (spinner) spinner.style.display = 'none';

        // When response is JSON, parse and update model select
        try {
          var data = JSON.parse(el);
          if (data.data && Array.isArray(data.data)) {
            updateModelSelect(data.data);
            showToast('&#10003; Models refreshed');
          }
        } catch (e) {
          // If it's HTML, it might be an error toast - just hide spinner
          showToast('Model refresh failed');
        }
      },
    });
  }

  function updateModelSelect(models) {
    var select = document.getElementById('model');
    if (!select) return;

    // Keep the currently selected value
    var currentValue = select.value;

    // Clear options (keep the placeholder)
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
        document.getElementById('settings-form').appendChild(notice);
      }
    }
    notice.innerHTML = message;
    notice.classList.add('fade-in');
    notice.classList.remove('fade-out');

    setTimeout(function () {
      notice.classList.add('fade-out');
      setTimeout(function () {
        notice.innerHTML = '';
      }, 600);
    }, 2000);
  }

  // — Test Connection button —
  function initTestConnection() {
    var btn = document.getElementById('test-connection-btn');
    if (!btn) return;

    // HTMX events handle the request lifecycle
    document.body.addEventListener('htmx:beforeSend', function (evt) {
      if (evt.detail && evt.detail.requestConfig && evt.detail.requestConfig.path === '/api/models') {
        var btn2 = document.getElementById('test-connection-btn');
        if (btn2) btn2.disabled = true;
        var result = document.getElementById('test-connection-result');
        if (result) result.innerHTML = '<span class="test-connection-pending">Testing...</span>';
      }
    });

    document.body.addEventListener('htmx:afterSwap', function (evt) {
      var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
      if (targetId === 'test-connection-result') {
        var btn2 = document.getElementById('test-connection-btn');
        if (btn2) btn2.disabled = false;

        // The target is now populated by HTMX swap from our server
        // or we need to handle the JSON response
        // In the htmx flow, response content-type matters
        // We'll need a server-side handler or use htmx:beforeOnLoad
      }
    });

    // For JSON responses from GET /api/models, HTMX won't auto-swap
    // We need to intercept and convert to our TestConnectionResult template
    document.body.addEventListener('htmx:beforeOnLoad', function (evt) {
      if (evt.detail && evt.detail.requestConfig && evt.detail.requestConfig.path === '/api/models' && evt.detail.xhr) {
        var xhr = evt.detail.xhr;
        var ct = xhr.getResponseHeader('Content-Type') || '';
        if (ct.indexOf('application/json') !== -1) {
          try {
            var data = JSON.parse(xhr.responseText);
            var resultEl = document.getElementById('test-connection-result');
            var btn2 = document.getElementById('test-connection-btn');
            if (btn2) btn2.disabled = false;

            if (resultEl) {
              if (data.data && Array.isArray(data.data)) {
                resultEl.innerHTML = '<span class="connection-ok">&#10003; Connection OK</span>';
                // Also refresh model list
                updateModelSelect(data.data);
              } else if (data.error) {
                resultEl.innerHTML = '<span class="connection-err">Connection failed: ' + escapeHtml(data.error) + '</span>';
              }
            }
          } catch (e) {
            var resultEl2 = document.getElementById('test-connection-result');
            if (resultEl2) {
              resultEl2.innerHTML = '<span class="connection-err">Connection failed</span>';
            }
          }
          // Prevent HTMX from trying to swap JSON
          evt.preventDefault();
        }
      }
    });
  }

  function escapeHtml(str) {
    var div = document.createElement('div');
    div.appendChild(document.createTextNode(str));
    return div.innerHTML;
  }

  // — Init on page load —
  function init() {
    // Only run on settings page
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
