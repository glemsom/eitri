// eitri-context — Browser island for context window utilization panel.
// Custom element <eitri-context> renders a compact progress bar with
// per-category breakdown toggled on click.

(function () {
  'use strict';

  // Debounce interval for rapid context updates (ms)
  var DEBOUNCE_MS = 100;

  function fmtNum(n) {
    return n.toLocaleString();
  }

  function barColorClass(pct) {
    if (pct < 60) return 'fill-green';
    if (pct <= 85) return 'fill-yellow';
    return 'fill-red';
  }

  function pctColorClass(pct) {
    if (pct < 60) return 'percent-green';
    if (pct <= 85) return 'percent-yellow';
    return 'percent-red';
  }

  function escapeHtml(str) {
    var div = document.createElement('div');
    div.appendChild(document.createTextNode(str));
    return div.innerHTML;
  }

  var ContextElement = window.customElements.get('eitri-context');
  if (ContextElement) return; // Already registered

  ContextElement = (function () {

    function ContextElement() {
      // Use Reflect.construct for proper subclassing
      var self = Reflect.construct(HTMLElement, [], ContextElement);
      self._contextWindow = 0;
      self._debounceTimer = null;
      self._pendingData = null;
      self._lastData = null;
      self._expanded = false;
      self._compactEl = null;
      self._expandedEl = null;
      self._idleEl = null;
      return self;
    }

    ContextElement.prototype = Object.create(HTMLElement.prototype);
    ContextElement.prototype.constructor = ContextElement;

    ContextElement.prototype.connectedCallback = function () {
      var cw = this.getAttribute('data-context-window');
      this._contextWindow = parseInt(cw, 10) || 0;

      // Wrap existing children as idle content
      this._idleEl = document.createElement('div');
      this._idleEl.className = 'context-idle';
      this._idleEl.textContent = 'No active run';

      // Compact view (always present, hidden in idle)
      this._compactEl = document.createElement('div');
      this._compactEl.className = 'context-compact';
      this._compactEl.style.display = 'none';
      this._compactEl.addEventListener('click', this._toggleExpand.bind(this));

      // Bar container
      var bar = document.createElement('div');
      bar.className = 'context-bar';
      var fill = document.createElement('div');
      fill.className = 'context-bar-fill';
      fill.id = this._makeId('fill');
      bar.appendChild(fill);
      this._compactEl.appendChild(bar);

      // Stats text
      var stats = document.createElement('span');
      stats.className = 'context-stats';
      stats.id = this._makeId('stats');
      this._compactEl.appendChild(stats);

      // Expanded view (hidden by default)
      this._expandedEl = document.createElement('div');
      this._expandedEl.className = 'context-expanded';
      this._expandedEl.id = this._makeId('expanded');

      // Clear children and append
      this.textContent = '';
      this.appendChild(this._idleEl);
      this.appendChild(this._compactEl);
      this.appendChild(this._expandedEl);

      // Listen for context-update events
      this._boundHandler = this._onContextUpdate.bind(this);
      this.addEventListener('context-update', this._boundHandler);
    };

    ContextElement.prototype.disconnectedCallback = function () {
      if (this._boundHandler) {
        this.removeEventListener('context-update', this._boundHandler);
        this._boundHandler = null;
      }
      if (this._debounceTimer) {
        clearTimeout(this._debounceTimer);
        this._debounceTimer = null;
      }
    };

    ContextElement.prototype._makeId = function (suffix) {
      return 'ctx-' + suffix;
    };

    ContextElement.prototype._toggleExpand = function () {
      this._expanded = !this._expanded;
      if (this._expandedEl) {
        this._expandedEl.classList.toggle('open', this._expanded);
      }
    };

    ContextElement.prototype._onContextUpdate = function (e) {
      var data = e.detail;
      if (!data) return;

      // Debounce rapid updates
      this._pendingData = data;
      if (this._debounceTimer) {
        clearTimeout(this._debounceTimer);
      }
      this._debounceTimer = setTimeout(this._applyPending.bind(this), DEBOUNCE_MS);
    };

    ContextElement.prototype._applyPending = function () {
      this._debounceTimer = null;
      var data = this._pendingData;
      this._pendingData = null;
      if (!data) return;
      this._lastData = data;

      // Transition from idle to active
      if (this._idleEl) this._idleEl.style.display = 'none';
      if (this._compactEl) this._compactEl.style.display = 'flex';

      this._renderCompact(data);
      if (this._expanded) {
        this._renderExpanded(data);
      }
    };

    ContextElement.prototype._renderCompact = function (data) {
      var cw = data.context_window || this._contextWindow || 1;
      var total = data.total_tokens || 0;
      var pct = Math.min(100, (total / cw) * 100);

      var fill = document.getElementById(this._makeId('fill'));
      if (fill) {
        fill.style.width = pct.toFixed(1) + '%';
        fill.className = 'context-bar-fill ' + barColorClass(pct);
      }

      var stats = document.getElementById(this._makeId('stats'));
      if (stats) {
        stats.textContent = fmtNum(total) + ' / ' + fmtNum(cw) + ' (' + pct.toFixed(0) + '%)';
      }
    };

    ContextElement.prototype._renderExpanded = function (data) {
      if (!this._expandedEl) return;

      var cw = data.context_window || this._contextWindow || 1;
      var total = data.total_tokens || 0;
      var prompt = data.prompt_tokens || 0;
      var completion = data.completion_tokens || 0;
      var system = data.system_tokens || 0;
      var history = data.history_tokens || 0;
      var skill = data.skill_tokens || 0;
      var pct = Math.min(100, (total / cw) * 100);

      // Use textContent/text nodes for user-facing numbers (no innerHTML from LLM data)
      this._expandedEl.textContent = '';

      function addRow(container, label, value, extraClass) {
        var row = document.createElement('div');
        row.className = 'context-category' + (extraClass ? ' ' + extraClass : '');
        var labelSpan = document.createElement('span');
        labelSpan.className = 'context-category-label';
        labelSpan.textContent = label;
        row.appendChild(labelSpan);
        var valSpan = document.createElement('span');
        valSpan.className = 'context-category-value';
        valSpan.textContent = fmtNum(value);
        row.appendChild(valSpan);
        container.appendChild(row);
      }

      // Overall percentage badge
      var pctRow = document.createElement('div');
      pctRow.style.display = 'flex';
      pctRow.style.justifyContent = 'space-between';
      pctRow.style.alignItems = 'center';
      pctRow.style.marginBottom = '0.3rem';
      var badge = document.createElement('span');
      badge.className = 'context-percent ' + pctColorClass(pct);
      badge.textContent = pct.toFixed(0) + '% used';
      pctRow.appendChild(badge);
      this._expandedEl.appendChild(pctRow);

      // Prompt row
      addRow(this._expandedEl, 'Prompt', prompt, '');
      // System sub-row
      addRow(this._expandedEl, 'System', system, 'context-subrow');
      // Skill sub-row
      addRow(this._expandedEl, 'Skills', skill, 'context-subrow');
      // History row
      addRow(this._expandedEl, 'History', history, '');
      // Completion row
      addRow(this._expandedEl, 'Completion', completion, '');
    };

    // Public method: reset to idle state (done/error/closed)
    ContextElement.prototype.resetToIdle = function () {
      this._lastData = null;
      this._pendingData = null;
      this._expanded = false;

      if (this._idleEl) this._idleEl.style.display = '';
      if (this._compactEl) this._compactEl.style.display = 'none';
      if (this._expandedEl) {
        this._expandedEl.classList.remove('open');
        this._expandedEl.textContent = '';
      }

      // Reset bar
      var fill = document.getElementById(this._makeId('fill'));
      if (fill) {
        fill.style.width = '0%';
        fill.className = 'context-bar-fill';
      }
      var stats = document.getElementById(this._makeId('stats'));
      if (stats) stats.textContent = '';
    };

    return ContextElement;
  })();

  window.customElements.define('eitri-context', ContextElement);

  // ---- Dispatch helper for eitri-stream.js ----

  // Dispatch a context-update custom event on the <eitri-context> element
  window.dispatchContextUpdate = function (data) {
    var el = document.querySelector('eitri-context');
    if (!el) return;
    var evt = new CustomEvent('context-update', {
      bubbles: false,
      cancelable: false,
      detail: data,
    });
    el.dispatchEvent(evt);
  };

  // Reset context panel to idle state
  window.resetContextPanel = function () {
    var el = document.querySelector('eitri-context');
    if (el && typeof el.resetToIdle === 'function') {
      el.resetToIdle();
    }
  };

})();
