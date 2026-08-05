// eitri-context — Custom element for rendering context usage panel.
// Receives ContextUpdate data via 'context-update' custom events and
// renders compact (progress bar + numbers) or expanded (category breakdown) views.
// Also shows a warning banner when context usage exceeds a configurable threshold.

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
        this._warningEl = null;
        this._contextWindow = 256000;
        this._warningThreshold = 75;
      }

      getActiveSessionId() {
        var m = window.location.pathname.match(/\/sessions\/([a-zA-Z0-9_-]+)/);
        return m ? m[1] : null;
      }

      connectedCallback() {
        // Guard re-entry so moving or re-rendering this element never
        // double-registers handlers (issue #1069).
        if (this._initialized) return;
        this._initialized = true;

        var self = this;
        self._contextWindow = parseInt(self.getAttribute('data-context-window'), 10) || 256000;
        self._warningThreshold = parseInt(self.getAttribute('data-warning-threshold'), 10) || 75;

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
            '<div class="context-category context-sub">' +
              '<span class="context-category-label">System</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value context-sub-value"></span>' +
            '</div>' +
            '<div class="context-category context-sub">' +
              '<span class="context-category-label">History</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value context-sub-value"></span>' +
            '</div>' +
            '<div class="context-category context-sub">' +
              '<span class="context-category-label">Skills</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value context-sub-value"></span>' +
            '</div>' +
            '<div class="context-category">' +
              '<span class="context-category-label">Completion</span>' +
              '<div class="context-category-bar"><div class="context-category-bar-fill"></div></div>' +
              '<span class="context-category-value"></span>' +
            '</div>' +
          '</div>' +
          '<div class="context-warning" style="display:none">' +
            '<span class="context-warning-text"></span>' +
            '<button class="context-warning-btn compact-btn">Compact now</button>' +
          '</div>';

        self._idleEl = self.querySelector('.context-idle');
        self._compactEl = self.querySelector('.context-compact');
        self._expandedEl = self.querySelector('.context-expanded');
        self._barFillEl = self.querySelector('.context-bar-fill');
        self._statsEl = self.querySelector('.context-stats');
        self._warningEl = self.querySelector('.context-warning');
        self._warningTextEl = self.querySelector('.context-warning-text');
        self._warningBtnEl = self.querySelector('.context-warning-btn');

        // Listen for context-update custom events
        self._onContextUpdate = function (e) {
          var data = e.detail;
          self._lastData = data;
          // Persist per session for re-hydration across session switches
          persistContextData(data);
          self._debouncedRender();
        };
        self.addEventListener('context-update', self._onContextUpdate);

        // Click compact view to toggle expanded
        self._onCompactClick = function () {
          self._expandedEl.classList.toggle('open');
        };
        self._compactEl.addEventListener('click', self._onCompactClick);

        // Click sidebar header to toggle content
        self._onHeaderClick = function () {
          if (self._lastData) {
            // Active: toggle expanded detail view
            self._expandedEl.classList.toggle('open');
          } else {
            // Idle: toggle idle message
            self._idleEl.classList.toggle('open');
          }
        };
        self._headerEl = document.querySelector('#context-panel .sidebar-header');
        if (self._headerEl) {
          self._headerEl.addEventListener('click', self._onHeaderClick);
        }

        // Wire up the warning banner's "Compact now" button
        self._onWarningBtnClick = function () {
          var sessionId = self.getActiveSessionId();
          if (!sessionId) return;
          htmx.ajax('POST', '/api/sessions/' + sessionId + '/compact', {
            target: '#error-toasts',
            swap: 'beforeend',
            handler: function () {
              // On successful compaction, hide the warning banner immediately
              self._warningEl.style.display = 'none';
            }
          });
        };
        if (self._warningBtnEl) {
          self._warningBtnEl.addEventListener('click', self._onWarningBtnClick);
        }

        // Also listen for HTMX events from the sidebar compact-btn
        self._onBodyAfterRequest = function (e) {
          // If the compact request completed successfully for this session
          if (e.detail.pathInfo.requestPath && e.detail.pathInfo.requestPath.indexOf('/compact') !== -1) {
            if (e.detail.successful) {
              self._warningEl.style.display = 'none';
            }
          }
        };
        document.body.addEventListener('htmx:afterRequest', self._onBodyAfterRequest);

        // Re-hydrate from persisted data when element is (re-)connected
        rehydrateIfAvailable(self);
      }

      disconnectedCallback() {
        // Tear down every document/body-level listener so a detached element
        // can be garbage-collected instead of leaking (issue #1069). The
        // _initialized flag is reset so re-connecting this same element
        // re-initializes cleanly instead of stacking handlers.
        if (this._onBodyAfterRequest) {
          document.body.removeEventListener('htmx:afterRequest', this._onBodyAfterRequest);
        }
        if (this._onHeaderClick && this._headerEl) {
          this._headerEl.removeEventListener('click', this._onHeaderClick);
        }
        if (this._debounceTimer) {
          window.clearTimeout(this._debounceTimer);
          this._debounceTimer = null;
        }
        this._initialized = false;
      }

      resetToIdle() {
        var self = this;
        self._lastData = null;
        self._idleEl.classList.remove('open');
        self._idleEl.style.display = '';
        self._compactEl.style.display = 'none';
        self._expandedEl.classList.remove('open');
        self._expandedEl.style.display = '';
        self._warningEl.style.display = 'none';
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
        this._renderWarning(data);
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

      _renderWarning(data) {
        var pct = data.context_window > 0
          ? Math.min(100, Math.round((data.total_tokens / data.context_window) * 100))
          : 0;

        if (pct >= this._warningThreshold) {
          this._warningTextEl.textContent = 'Context at ' + pct + '% — compaction recommended';
          this._warningEl.style.display = 'flex';
        } else {
          this._warningEl.style.display = 'none';
        }
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
