// eitri-composer — Browser island for keyboard handling and completion menu.
// Manages /skill and @file completions with debounce, ARIA, and sequence gating.
(function () {
  'use strict';

  class EitriComposer extends HTMLElement {
    connectedCallback() {
      // Already initialized
      if (this._initialized) return;
      this._initialized = true;

      this.textarea = this.querySelector('.chat-input');
      this.form = this.querySelector('form');
      if (!this.textarea || !this.form) return;

      this.sessionId = this._extractSessionId();
      this.menuEl = null;
      this.menuType = null; // 'skill' or 'file'
      this.selectedIdx = -1;
      this.sequence = 0;

      this._setupMenu();
      this._bindEvents();
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
      this.textarea.addEventListener('input', () => this._onInput());
      this.textarea.addEventListener('keydown', (e) => this._onKeydown(e));
      this.textarea.addEventListener('blur', () => setTimeout(() => this._closeMenu(), 200));
      this.textarea.setAttribute('aria-controls', 'completion-menu');
      this.textarea.setAttribute('aria-autocomplete', 'list');
      this.textarea.setAttribute('aria-expanded', 'false');
    }

    _onInput() {
      const { text, pos } = this._getTextAndPos();
      const token = this._getTokenAtCursor(text, pos);

      if (!token) {
        this._closeMenu();
        return;
      }

      // Only trigger completions at the start of a word
      const charBefore = pos > 0 ? text[pos - 1] : ' ';
      if (charBefore !== ' ' && charBefore !== '\n' && token.start !== 0) {
        this._closeMenu();
        return;
      }

      if (token.prefix === '/') {
        const query = token.value.slice(1); // after /
        this._fetchCompletions('skill', query, token);
      } else if (token.prefix === '@') {
        const query = token.value.slice(1); // after @
        this._fetchCompletions('file', query, token);
      } else {
        this._closeMenu();
      }
    }

    _getTextAndPos() {
      const text = this.textarea.value;
      const pos = this.textarea.selectionStart;
      return { text, pos };
    }

    // Returns the token (prefix + value) at cursor, or null
    _getTokenAtCursor(text, pos) {
      if (pos <= 0) return null;

      // Look backwards from cursor to find start of token
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

      const value = text.slice(start, pos);
      return { prefix: firstChar, value, start };
    }

    _fetchCompletions(type, query, token) {
      const seq = ++this.sequence;
      const currentSeq = seq;

      let url;
      if (type === 'skill') {
        url = `/api/sessions/${this.sessionId}/complete/skills?q=${encodeURIComponent(query)}`;
      } else {
        url = `/api/sessions/${this.sessionId}/complete/files?q=${encodeURIComponent(query)}`;
      }

      fetch(url)
        .then(r => r.json())
        .then(data => {
          if (currentSeq !== this.sequence) return; // stale
          if (!data.items || data.items.length === 0) {
            this._closeMenu();
            return;
          }
          this._renderMenu(type, data.items, query, token);
        })
        .catch(() => {
          if (currentSeq === this.sequence) this._closeMenu();
        });
    }

    _renderMenu(type, items, query, token) {
      this.menuType = type;
      this.selectedIdx = -1;
      this._token = token;

      this.menuEl.innerHTML = '';
      this.menuEl.style.display = 'block';
      this.textarea.setAttribute('aria-expanded', 'true');

      items.forEach((item, idx) => {
        const opt = document.createElement('div');
        opt.setAttribute('role', 'option');
        opt.setAttribute('aria-selected', 'false');
        opt.className = 'completion-item';
        if (idx === this.selectedIdx) {
          opt.classList.add('selected');
          opt.setAttribute('aria-selected', 'true');
          this.textarea.setAttribute('aria-activedescendant', 'completion-item-' + idx);
        }
        opt.id = 'completion-item-' + idx;
        opt.dataset.index = idx;

        if (type === 'skill') {
          opt.innerHTML = '<span class="completion-label">' + this._escapeHtml(item.name) + '</span>' +
            '<span class="completion-desc">' + this._escapeHtml(item.description || '') + '</span>';
          opt.dataset.value = item.name;
        } else {
          opt.innerHTML = '<span class="completion-label">' + this._escapeHtml(item.path) + '</span>' +
            '<span class="completion-desc">' + item.kind + '</span>';
          opt.dataset.value = item.path;
        }

        opt.addEventListener('mousedown', (e) => {
          e.preventDefault();
          this._selectItem(idx);
        });

        this.menuEl.appendChild(opt);
      });
    }

    _onKeydown(e) {
      if (this.menuEl.style.display !== 'block') {
        // Escape cancels active run
        if (e.key === 'Escape') {
          const stopBtn = document.getElementById('stop-btn');
          if (stopBtn && stopBtn.style.display !== 'none') {
            stopBtn.click();
            e.preventDefault();
          }
        }
        return;
      }

      const items = this.menuEl.querySelectorAll('.completion-item');
      if (!items.length) return;

      switch (e.key) {
        case 'ArrowDown':
        case 'Tab':
          e.preventDefault();
          this.selectedIdx = Math.min(this.selectedIdx + 1, items.length - 1);
          this._highlightItem(items);
          break;

        case 'ArrowUp':
          e.preventDefault();
          this.selectedIdx = Math.max(this.selectedIdx - 1, 0);
          this._highlightItem(items);
          break;

        case 'Enter':
          e.preventDefault();
          if (this.selectedIdx >= 0) {
            this._selectItem(this.selectedIdx);
          }
          break;

        case 'Escape':
          e.preventDefault();
          this._closeMenu();
          break;
      }
    }

    _highlightItem(items) {
      items.forEach((el, idx) => {
        el.classList.toggle('selected', idx === this.selectedIdx);
        el.setAttribute('aria-selected', idx === this.selectedIdx ? 'true' : 'false');
      });
      if (this.selectedIdx >= 0) {
        const id = 'completion-item-' + this.selectedIdx;
        this.textarea.setAttribute('aria-activedescendant', id);
        items[this.selectedIdx].scrollIntoView({ block: 'nearest' });
      }
    }

    _selectItem(index) {
      const items = this.menuEl.querySelectorAll('.completion-item');
      if (index < 0 || index >= items.length) return;

      const item = items[index];
      const value = item.dataset.value;
      const token = this._token;
      if (!token) return;

      const text = this.textarea.value;
      const before = text.slice(0, token.start);
      const after = text.slice(this.textarea.selectionStart);

      // Determine insertion: skill names get trailing space, dirs keep menu open
      const isDir = item.querySelector('.completion-desc')?.textContent === 'dir';
      const suffix = isDir ? '' : ' ';

      this.textarea.value = before + value + suffix + after;
      const newPos = before.length + value.length + suffix.length;
      this.textarea.setSelectionRange(newPos, newPos);
      this.textarea.focus();

      if (isDir) {
        // Keep menu open for directory completion
        this._token = { prefix: '@', value: value + '/', start: token.start };
        this._onInput();
      } else {
        this._closeMenu();
      }

      // Trigger HTMX validation
      this.textarea.dispatchEvent(new Event('input', { bubbles: true }));
    }

    _closeMenu() {
      this.menuEl.style.display = 'none';
      this.menuEl.innerHTML = '';
      this.menuType = null;
      this.selectedIdx = -1;
      this._token = null;
      this.textarea.setAttribute('aria-expanded', 'false');
      this.textarea.removeAttribute('aria-activedescendant');
    }

    _escapeHtml(str) {
      const div = document.createElement('div');
      div.textContent = str;
      return div.innerHTML;
    }
  }

  customElements.define('eitri-composer', EitriComposer);
})();
