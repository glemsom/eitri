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
        this._contextWindow = 256000;
      }

      connectedCallback() {
        var self = this;
        self._contextWindow = parseInt(self.getAttribute('data-context-window'), 10) || 256000;

        // Build inner DOM (no template fallback — JS owns the full DOM)
        self.innerHTML =
          '<div class="context-idle">No active run</div>' +
          '<div class="context-compact" style="display:none">' +
            '<div class="context-bar">' +
              '<div class="context-bar-fill"></div>' +
            '</div>' +
            '<span class="context-stats"></span>' +
          '</div>' +
          '<div class="context-expanded">' +
            '<div class="context-category">' +
              '<span class="context-category-label">Prompt</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value"></span>' +
            '</div>' +
            '<div class="context-category context-sub" style="padding-left:1rem">' +
              '<span class="context-category-label">System</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value context-sub-value"></span>' +
            '</div>' +
            '<div class="context-category context-sub" style="padding-left:1rem">' +
              '<span class="context-category-label">History</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value context-sub-value"></span>' +
            '</div>' +
            '<div class="context-category context-sub" style="padding-left:1rem">' +
              '<span class="context-category-label">Skills</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value context-sub-value"></span>' +
            '</div>' +
            '<div class="context-category">' +
              '<span class="context-category-label">Completion</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value"></span>' +
            '</div>' +
          '</div>';

        self._idleEl = self.querySelector('.context-idle');
        self._compactEl = self.querySelector('.context-compact');
        self._expandedEl = self.querySelector('.context-expanded');
        self._barFillEl = self.querySelector('.context-bar-fill');
        self._statsEl = self.querySelector('.context-stats');

        // Listen for context-update custom events
        self.addEventListener('context-update', function (e) {
          var data = e.detail;
          self._lastData = data;
          // Persist per session for re-hydration across session switches
          persistContextData(data);
          self._debouncedRender();
        });

        // Click compact view to toggle expanded
        self._compactEl.addEventListener('click', function () {
          self._expandedEl.classList.toggle('open');
        });

        // Click sidebar header to toggle content
        var header = document.querySelector('#context-panel .sidebar-header');
        if (header) {
          header.addEventListener('click', function () {
            if (self._lastData) {
              // Active: toggle expanded detail view
              self._expandedEl.classList.toggle('open');
            } else {
              // Idle: toggle idle message
              self._idleEl.classList.toggle('open');
            }
          });
        }

        // Re-hydrate from persisted data when element is (re-)connected
        rehydrateIfAvailable(self);
      }

      resetToIdle() {
        var self = this;
        self._lastData = null;
        self._idleEl.classList.remove('open');
        self._idleEl.style.display = '';
        self._compactEl.style.display = 'none';
        self._expandedEl.classList.remove('open');
        self._expandedEl.style.display = '';
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
        this._idleEl.classList.remove('open');
        this._idleEl.style.display = '';
        this._compactEl.style.display = 'flex';
        // Remove any inline display override from resetToIdle so CSS .open class works
        this._expandedEl.style.display = '';

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
        var cw = data.context_window;
        if (!cw) return;

        // Category definitions: [selectorKey, tokensKey]
        var categories = [
          { sel: '.context-category:nth-child(1) .context-category-value', tokens: 'prompt_tokens' },
          { sel: '.context-category:nth-child(2) .context-sub-value', tokens: 'system_tokens' },
          { sel: '.context-category:nth-child(3) .context-sub-value', tokens: 'history_tokens' },
          { sel: '.context-category:nth-child(4) .context-sub-value', tokens: 'skill_tokens' },
          { sel: '.context-category:nth-child(5) .context-category-value', tokens: 'completion_tokens' },
        ];

        var self = this;
        categories.forEach(function (cat, idx) {
          var tokens = data[cat.tokens] || 0;
          var valEl = self._expandedEl.querySelector(cat.sel);
          if (valEl) {
            valEl.textContent = tokens.toLocaleString();
          }

          // Build mini bar for this category
          var pct = cw > 0 ? Math.min(100, Math.round((tokens / cw) * 100)) : 0;
          var barEl = self._expandedEl.querySelectorAll('.context-category-bar-fill')[idx];
          if (barEl) {
            barEl.style.width = pct + '%';
            barEl.classList.remove('fill-green', 'fill-yellow', 'fill-red');
            if (pct < 60) {
              barEl.classList.add('fill-green');
            } else if (pct < 85) {
              barEl.classList.add('fill-yellow');
            } else {
              barEl.classList.add('fill-red');
            }
          }
        });
      }
    }

    return EitriContext;
  })();

  // ── Persistence layer ──────────────────────────────────────
  // Store last context data per session so it survives session switches.
  // Keyed by active session ID from the URL path.

  function getActiveSessionId() {
    var m = window.location.pathname.match(/\/sessions\/([a-zA-Z0-9_-]+)/);
    return m ? m[1] : null;
  }

  var STORAGE_KEY_PREFIX = 'eitri-context-';

  function persistContextData(data) {
    var sid = getActiveSessionId();
    if (!sid) return;
    try {
      sessionStorage.setItem(STORAGE_KEY_PREFIX + sid, JSON.stringify(data));
    } catch (e) {
      // sessionStorage may be full or unavailable — fall through
    }
  }

  function rehydrateIfAvailable(el) {
    var sid = getActiveSessionId();
    if (!sid) return;
    try {
      var raw = sessionStorage.getItem(STORAGE_KEY_PREFIX + sid);
      if (!raw) return;
      var data = JSON.parse(raw);
      if (!data || !data.total_tokens) return;
      el._lastData = data;
      el._debouncedRender();
    } catch (e) {
      // Corrupted data — ignore
    }
  }

  function clearContextData(sid) {
    if (!sid) {
      sid = getActiveSessionId();
    }
    if (!sid) return;
    try {
      sessionStorage.removeItem(STORAGE_KEY_PREFIX + sid);
    } catch (e) {
      // ignore
    }
  }

  // Register custom element
  customElements.define('eitri-context', EitriContext);

  // Global helpers for eitri-stream.js to call
  window.dispatchContextUpdate = function (data) {
    var el = document.querySelector('eitri-context');
    if (!el) return;
    el.dispatchEvent(new CustomEvent('context-update', { detail: data }));
  };

  window.resetContextPanel = function () {
    // Clear persisted data for current session
    clearContextData();
    var el = document.querySelector('eitri-context');
    if (!el) return;
    el.resetToIdle();
  };

})();
