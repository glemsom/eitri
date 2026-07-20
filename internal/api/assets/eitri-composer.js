// eitri-composer — Browser island for keyboard handling, completion menu, and mobile keyboard visibility.
// Manages /skill and @file completions with debounce, ARIA, and sequence gating.
// On mobile (≤768px), tracks visualViewport to keep composer visible above virtual keyboard.
(function () {
  'use strict';

  class EitriComposer extends HTMLElement {
    connectedCallback() {
      if (this._initialized) return;

      this.textarea = this.querySelector('.chat-input');
      this.form = this.querySelector('form');
      if (!this.textarea || !this.form) {
        if (!this._initScheduled) {
          this._initScheduled = true;
          window.requestAnimationFrame(() => {
            this._initScheduled = false;
            this.connectedCallback();
          });
        }
        return;
      }

      this._initialized = true;
      this.sessionId = this._extractSessionId();

      // Auto-focus the chat input on page load and HTMX swaps
      if (!this.textarea.disabled) {
        this.textarea.focus();
      }
      this.menuEl = null;
      this.menuType = null;
      this.selectedIdx = -1;
      this.sequence = 0;
      this.debounceDelayMs = 100;
      this.debounceTimer = null;
      this._token = null;

      this._setupMenu();
      this._bindEvents();
      this._trackComposerHeight();
      this._initMobileKeyboard();
      // Set initial height in case the textarea has pre-filled content
      this._autoResize();
    }

    _trackComposerHeight() {
      var parent = this.parentElement;
      if (!parent) return;
      var updateHeight = function(el) {
        var height = el.offsetHeight;
        parent.style.setProperty('--composer-height', height + 'px');
      };
      updateHeight(this);
      var ro = new ResizeObserver(function() {
        updateHeight(this);
      }.bind(this));
      ro.observe(this);
      this._composerResizeObserver = ro;
    }

    _initMobileKeyboard() {
      // On iOS/Safari, the layout viewport does not resize when the virtual keyboard
      // opens, but visualViewport does. We adjust the composer bottom position so
      // it stays visible above the keyboard. Only active on narrow viewports.
      if (!window.visualViewport) return;

      var composerEl = this;
      var chatViewEl = this.closest('#chat-view');
      if (!chatViewEl) return;

      var storedBottom = '';
      var viewportHandler = function () {
        var isMobile = window.innerWidth <= 768;
        if (!isMobile) {
          if (storedBottom) {
            chatViewEl.style.setProperty('--composer-bottom', storedBottom);
            storedBottom = '';
            composerEl.style.position = '';
            composerEl.style.bottom = '';
            composerEl.style.left = '';
            composerEl.style.right = '';
            composerEl.style.zIndex = '';
          }
          return;
        }

        var vvHeight = window.visualViewport.height;
        var lvHeight = window.innerHeight;
        var diff = lvHeight - vvHeight;

        if (diff > 80) {
          // Keyboard likely open: pin composer above keyboard
          if (!storedBottom) storedBottom = '0px';
          composerEl.style.position = 'fixed';
          composerEl.style.bottom = diff + 'px';
          composerEl.style.left = '0';
          composerEl.style.right = '0';
          composerEl.style.zIndex = '100';
          chatViewEl.style.setProperty('--composer-bottom', diff + 'px');
        } else {
          // Keyboard closed: restore
          if (storedBottom) {
            chatViewEl.style.setProperty('--composer-bottom', storedBottom);
            storedBottom = '';
          }
          composerEl.style.position = '';
          composerEl.style.bottom = '';
          composerEl.style.left = '';
          composerEl.style.right = '';
          composerEl.style.zIndex = '';
        }
      };

      window.visualViewport.addEventListener('resize', viewportHandler);
      window.visualViewport.addEventListener('scroll', viewportHandler);
      this._visualViewportHandler = viewportHandler;
    }

    /** Auto-expand the textarea as the user types past the initial 3-line height.
     * When the textarea is empty, resets to default height (3 lines via CSS min-height). */
    _autoResize() {
      var ta = this.textarea;
      // Reset to default when empty
      if (ta.value === '') {
        ta.style.height = '';
        return;
      }
      // Temporarily collapse to measure natural scrollHeight
      ta.style.height = 'auto';
      var newHeight = ta.scrollHeight;
      // Clamp to max-height (50vh) — matching the CSS
      var maxHeight = parseInt(getComputedStyle(ta).maxHeight, 10);
      if (maxHeight && newHeight > maxHeight) {
        ta.style.height = maxHeight + 'px';
      } else {
        ta.style.height = newHeight + 'px';
      }
    }

    disconnectedCallback() {
      if (this._handleDocumentKeydown) {
        document.removeEventListener('keydown', this._handleDocumentKeydown);
      }
      if (this._composerResizeObserver) {
        this._composerResizeObserver.disconnect();
      }
      if (this._visualViewportHandler && window.visualViewport) {
        window.visualViewport.removeEventListener('resize', this._visualViewportHandler);
        window.visualViewport.removeEventListener('scroll', this._visualViewportHandler);
      }
    }



    _extractSessionId() {
      const action = this.form.getAttribute('hx-post') || '';
      const m = action.match(/\/api\/sessions\/([^/]+)\/chat/);
      return m ? m[1] : '';
    }

    _setupMenu() {
      this.menuEl = document.createElement('div');
      this.menuEl.id = 'completion-menu';
      this.menuEl.className = 'completion-menu';
      this.menuEl.setAttribute('role', 'listbox');
      this.menuEl.setAttribute('aria-label', 'Completion suggestions');
      this.menuEl.style.display = 'none';
      this.textarea.parentNode.insertBefore(this.menuEl, this.textarea.nextSibling);
    }

    _bindEvents() {
      this.textarea.addEventListener('input', () => {
        this._onInput();
        this._autoResize();
      });
      this.textarea.addEventListener('keydown', (e) => this._onKeydown(e));
      this.textarea.addEventListener('blur', () => setTimeout(() => this._closeMenu(), 200));
      this.textarea.setAttribute('aria-controls', 'completion-menu');
      this.textarea.setAttribute('aria-autocomplete', 'list');
      this.textarea.setAttribute('aria-expanded', 'false');

      this._handleDocumentKeydown = (e) => {
        if (e.key !== 'Escape') return;
        if (this.menuEl.style.display === 'block' && document.activeElement === this.textarea) {
          return;
        }
        if (this._cancelActiveRun()) {
          e.preventDefault();
        }
      };
      document.addEventListener('keydown', this._handleDocumentKeydown);
    }

    _onInput() {
      window.clearTimeout(this.debounceTimer);

      const { text, pos } = this._getTextAndPos();
      const token = this._getTokenAtCursor(text, pos);
      if (!token || !this._isTokenBoundary(text, token.start)) {
        this._closeMenu();
        return;
      }

      const type = token.prefix === '/' ? 'skill' : token.prefix === '@' ? 'file' : null;
      if (!type) {
        this._closeMenu();
        return;
      }

      const query = token.value.slice(1);
      const seq = ++this.sequence;
      this.debounceTimer = window.setTimeout(() => {
        this._fetchCompletions(type, query, token, seq);
      }, this.debounceDelayMs);
    }

    _isTokenBoundary(text, start) {
      if (start === 0) return true;
      const before = text[start - 1];
      return before === ' ' || before === '\n' || before === '\t';
    }

    _getTextAndPos() {
      return {
        text: this.textarea.value,
        pos: this.textarea.selectionStart,
      };
    }

    _getTokenAtCursor(text, pos) {
      if (pos <= 0) return null;

      let start = pos;
      while (start > 0) {
        const ch = text[start - 1];
        if (ch === '/' || ch === '@') {
          start--;
          break;
        }
        if (ch === ' ' || ch === '\n' || ch === '\t') break;
        start--;
      }

      if (start >= pos || start < 0) return null;
      const firstChar = text[start];
      if (firstChar !== '/' && firstChar !== '@') return null;

      return {
        prefix: firstChar,
        value: text.slice(start, pos),
        start,
      };
    }

    _fetchCompletions(type, query, token, seq) {
      if (seq !== this.sequence) return;

      const url = type === 'skill'
        ? `/api/sessions/${this.sessionId}/complete/skills?q=${encodeURIComponent(query)}`
        : `/api/sessions/${this.sessionId}/complete/files?q=${encodeURIComponent(query)}`;

      fetch(url)
        .then((r) => r.json())
        .then((data) => {
          if (seq !== this.sequence) return;
          if (!data.items || data.items.length === 0) {
            this._closeMenu();
            return;
          }
          this._renderMenu(type, data.items, token);
        })
        .catch(() => {
          if (seq === this.sequence) this._closeMenu();
        });
    }

    _renderMenu(type, items, token) {
      this.menuType = type;
      this.selectedIdx = -1;
      this._token = token;

      this.menuEl.replaceChildren();
      this.menuEl.style.display = 'block';
      this.textarea.setAttribute('aria-expanded', 'true');

      items.forEach((item, idx) => {
        const opt = document.createElement('div');
        opt.id = 'completion-item-' + idx;
        opt.className = 'completion-item';
        opt.dataset.index = String(idx);
        opt.setAttribute('role', 'option');
        opt.setAttribute('aria-selected', 'false');

        const label = document.createElement('span');
        label.className = 'completion-label';
        label.textContent = type === 'skill' ? item.name : item.path;

        const desc = document.createElement('span');
        desc.className = 'completion-desc';
        desc.textContent = type === 'skill' ? (item.description || '') : item.kind;

        opt.dataset.value = type === 'skill' ? item.name : item.path;
        if (type === 'file') {
          opt.dataset.kind = item.kind;
        }

        opt.appendChild(label);
        opt.appendChild(desc);
        opt.addEventListener('mousedown', (e) => {
          e.preventDefault();
          this._selectItem(idx);
        });

        this.menuEl.appendChild(opt);
      });
    }

    _onKeydown(e) {
      const menuOpen = this.menuEl.style.display === 'block';
      if (!menuOpen) {
        if (e.key === 'Escape') {
          if (this._cancelActiveRun()) {
            e.preventDefault();
          }
          return;
        }

        if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey) {
          e.preventDefault();
          this._submitForm();
        }
        return;
      }

      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          this._moveSelection(1);
          break;

        case 'ArrowUp':
          e.preventDefault();
          this._moveSelection(-1);
          break;

        case 'Tab':
          e.preventDefault();
          this._moveSelection(e.shiftKey ? -1 : 1);
          break;

        case 'Enter':
          e.preventDefault();
          if (this.selectedIdx >= 0) {
            this._selectItem(this.selectedIdx);
          } else {
            // No selection — close menu and submit form
            this._closeMenu();
            this._submitForm();
          }
          break;

        case 'Escape':
          e.preventDefault();
          this._closeMenu();
          break;
      }
    }

    _submitForm() {
      if (this.textarea.disabled) return;
      this._closeMenu();
      const sendBtn = this.form.querySelector('#send-btn');
      if (sendBtn) {
        sendBtn.click();
        return;
      }
      if (typeof this.form.requestSubmit === 'function') {
        this.form.requestSubmit();
        return;
      }
      this.form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    }

    _cancelActiveRun() {
      const stopBtn = document.getElementById('stop-btn');
      if (!stopBtn || stopBtn.classList.contains('stop-hidden')) {
        return false;
      }
      stopBtn.click();
      return true;
    }

    _moveSelection(delta) {
      const items = this.menuEl.querySelectorAll('.completion-item');
      if (!items.length) return;

      if (this.selectedIdx < 0) {
        this.selectedIdx = delta < 0 ? items.length - 1 : 0;
      } else {
        this.selectedIdx = Math.max(0, Math.min(this.selectedIdx + delta, items.length - 1));
      }
      this._highlightItem(items);
    }

    _highlightItem(items) {
      items.forEach((el, idx) => {
        const selected = idx === this.selectedIdx;
        el.classList.toggle('selected', selected);
        el.setAttribute('aria-selected', selected ? 'true' : 'false');
      });
      if (this.selectedIdx >= 0) {
        const id = 'completion-item-' + this.selectedIdx;
        this.textarea.setAttribute('aria-activedescendant', id);
        items[this.selectedIdx].scrollIntoView({ block: 'nearest' });
      } else {
        this.textarea.removeAttribute('aria-activedescendant');
      }
    }

    _selectItem(index) {
      const items = this.menuEl.querySelectorAll('.completion-item');
      if (index < 0 || index >= items.length) return;

      const item = items[index];
      const token = this._token;
      if (!token) return;

      const value = item.dataset.value || '';
      const kind = item.dataset.kind || '';
      const keepMenuOpen = token.prefix === '@' && kind === 'dir';
      const suffix = keepMenuOpen ? '' : ' ';

      const before = this.textarea.value.slice(0, token.start);
      const after = this.textarea.value.slice(this.textarea.selectionStart);
      this.textarea.value = before + token.prefix + value + suffix + after;

      const newPos = before.length + token.prefix.length + value.length + suffix.length;
      this.textarea.setSelectionRange(newPos, newPos);
      this.textarea.focus();
      this.textarea.dispatchEvent(new Event('input', { bubbles: true }));

      if (!keepMenuOpen) {
        this._closeMenu();
        // Auto-submit for skill activation — if user selected a skill, no prompt needed
        if (this.menuType === 'skill') {
          this._submitForm();
        }
      }
    }

    _closeMenu() {
      window.clearTimeout(this.debounceTimer);
      this.sequence++;
      this.menuEl.style.display = 'none';
      this.menuEl.replaceChildren();
      this.menuType = null;
      this.selectedIdx = -1;
      this._token = null;
      this.textarea.setAttribute('aria-expanded', 'false');
      this.textarea.removeAttribute('aria-activedescendant');
    }
  }

  customElements.define('eitri-composer', EitriComposer);
})();

// Listen for run-started event from HTMX HX-Trigger header
// Uses CSS class toggle instead of outerHTML swap (issue #103).
(function () {
  'use strict';
  document.addEventListener('eitri:runStarted', function () {
    var input = document.getElementById('chat-input');
    var sendBtn = document.getElementById('send-btn');
    var stopBtn = document.getElementById('stop-btn');
    if (input) {
      input.value = '';
      input.disabled = true;
    }
    if (sendBtn) {
      sendBtn.disabled = true;
      sendBtn.classList.add('send-hidden');
    }
    if (stopBtn) {
      stopBtn.classList.remove('stop-hidden');
    }
  });
})();
