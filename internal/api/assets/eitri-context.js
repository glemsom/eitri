// eitri-context — Custom element for rendering context usage panel.
// Receives ContextUpdate data via 'context-update' custom events and
// renders compact (progress bar + numbers) or expanded (category breakdown) views.

(function () {
  'use strict';

  var DEBOUNCE_MS = 100;

  var EitriContext = (function () {

    // Use a real class extending HTMLElement
    class EitriContext extends HTMLElement {
      constructor() {
        super();
        this._debounceTimer = null;
        this._lastData = null;
        this._compactEl = null;
        this._idleEl = null;
        this._expandedEl = null;
        this._barFillEl = null;
        this._statsEl = null;
        this._contextWindow = 128000;
      }

      connectedCallback() {
        var self = this;
        self._contextWindow = parseInt(self.getAttribute('data-context-window'), 10) || 128000;

        // Build inner DOM
        self.innerHTML =
          '<div class="context-idle">No active run</div>' +
          '<div class="context-compact" style="display:none">' +
            '<div class="context-bar">' +
              '<div class="context-bar-fill"></div>' +
            '</div>' +
            '<span class="context-stats"></span>' +
          '</div>' +
          '<div class="context-expanded">' +
            '<div class="context-category"><span class="context-category-label">Prompt</span><span class="context-category-value"></span></div>' +
            '<div class="context-category context-sub" style="padding-left:1rem"><span class="context-category-label">System</span><span class="context-category-value context-sub-value"></span></div>' +
            '<div class="context-category context-sub" style="padding-left:1rem"><span class="context-category-label">History</span><span class="context-category-value context-sub-value"></span></div>' +
            '<div class="context-category context-sub" style="padding-left:1rem"><span class="context-category-label">Skills</span><span class="context-category-value context-sub-value"></span></div>' +
            '<div class="context-category"><span class="context-category-label">Completion</span><span class="context-category-value"></span></div>' +
          '</div>';

        self._idleEl = self.querySelector('.context-idle');
        self._compactEl = self.querySelector('.context-compact');
        self._expandedEl = self.querySelector('.context-expanded');
        self._barFillEl = self.querySelector('.context-bar-fill');
        self._statsEl = self.querySelector('.context-stats');

        // Listen for context-update custom events
        self.addEventListener('context-update', function (e) {
          self._lastData = e.detail;
          self._debouncedRender();
        });

        // Click compact view to toggle expanded
        self._compactEl.addEventListener('click', function () {
          self._expandedEl.classList.toggle('open');
        });

        // Click sidebar header to toggle expanded
        var header = document.querySelector('#context-panel .sidebar-header');
        if (header) {
          header.addEventListener('click', function () {
            self._expandedEl.classList.toggle('open');
          });
        }
      }

      resetToIdle() {
        var self = this;
        self._lastData = null;
        self._idleEl.style.display = 'block';
        self._compactEl.style.display = 'none';
        self._expandedEl.classList.remove('open');
        self._expandedEl.style.display = 'none';
      }

      _debouncedRender() {
        var self = this;
        if (self._debounceTimer) {
          clearTimeout(self._debounceTimer);
        }
        self._debounceTimer = window.setTimeout(function () {
          self._debounceTimer = null;
          self._render();
        }, DEBOUNCE_MS);
      }

      _render() {
        var data = this._lastData;
        if (!data) return;

        // Use actual context_window from data, fallback to attribute value
        var cw = data.context_window || this._contextWindow;
        data.context_window = cw;

        // Transition from idle to active
        this._idleEl.style.display = 'none';
        this._compactEl.style.display = 'flex';
        this._expandedEl.style.display = 'block';

        this._renderCompact(data);
        this._renderExpanded(data);
      }

      _renderCompact(data) {
        var pct = data.context_window > 0
          ? Math.min(100, Math.round((data.total_tokens / data.context_window) * 100))
          : 0;

        this._barFillEl.style.width = pct + '%';

        // Color class
        this._barFillEl.classList.remove('fill-green', 'fill-yellow', 'fill-red');
        if (pct < 60) {
          this._barFillEl.classList.add('fill-green');
        } else if (pct < 85) {
          this._barFillEl.classList.add('fill-yellow');
        } else {
          this._barFillEl.classList.add('fill-red');
        }

        // Stats text: "12,847 / 128K (10%)"
        var totalStr = data.total_tokens.toLocaleString();
        var windowStr = data.context_window >= 1000
          ? Math.round(data.context_window / 1000) + 'K'
          : String(data.context_window);
        this._statsEl.textContent = totalStr + ' / ' + windowStr + ' (' + pct + '%)';
      }

      _renderExpanded(data) {
        // Prompt row
        var promptVal = this._expandedEl.querySelector('.context-category:first-child .context-category-value');
        if (promptVal) {
          promptVal.textContent = (data.prompt_tokens || 0).toLocaleString();
        }

        // Sub-rows
        var subValues = this._expandedEl.querySelectorAll('.context-sub-value');
        var fields = ['system_tokens', 'history_tokens', 'skill_tokens'];
        fields.forEach(function (key, i) {
          if (subValues[i]) {
            subValues[i].textContent = (data[key] || 0).toLocaleString();
          }
        });

        // Completion row (last category)
        var catValues = this._expandedEl.querySelectorAll('.context-category .context-category-value');
        if (catValues.length > 0) {
          catValues[catValues.length - 1].textContent = (data.completion_tokens || 0).toLocaleString();
        }
      }
    }

    return EitriContext;
  })();

  // Register custom element
  customElements.define('eitri-context', EitriContext);

  // Global helpers for eitri-stream.js to call
  window.dispatchContextUpdate = function (data) {
    var el = document.querySelector('eitri-context');
    if (!el) return;
    el.dispatchEvent(new CustomEvent('context-update', { detail: data }));
  };

  window.resetContextPanel = function () {
    var el = document.querySelector('eitri-context');
    if (!el) return;
    el.resetToIdle();
  };

})();
