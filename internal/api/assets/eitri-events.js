// eitri-events — Browser-level event stream for real-time UI updates.
// Connects to /api/events once on page load and receives session-level
// events (e.g., session status changes) that affect the global UI state.

(function () {
  'use strict';

  var eventsUrl = '/api/events';
  var eventSource = null;
  var reconnectTimer = null;
  var maxReconnectDelay = 30000; // 30s
  var reconnectDelay = 1000; // starts at 1s

  function connect() {
    if (eventSource) {
      eventSource.close();
    }

    eventSource = new EventSource(eventsUrl);

    eventSource.onopen = function () {
      reconnectDelay = 1000; // Reset on successful connection
    };

    eventSource.onmessage = function (event) {
      try {
        var data = JSON.parse(event.data);
        handleEvent(data);
      } catch (err) {
        console.warn('[eitri-events] Failed to parse event data:', err);
      }
    };

    eventSource.onerror = function () {
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
      scheduleReconnect();
    };
  }

  function scheduleReconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
    }
    reconnectTimer = setTimeout(function () {
      reconnectTimer = null;
      connect();
    }, reconnectDelay);
    // Exponential backoff, capped at maxReconnectDelay
    reconnectDelay = Math.min(reconnectDelay * 2, maxReconnectDelay);
  }

  function handleEvent(data) {
    switch (data.type) {
      case 'session_status':
        // A session's status changed (e.g., running -> idle after run completes).
        // Fetch updated session tabs via HTMX OOB swap.
        var sessionId = data.data && data.data.session_id;
        if (sessionId) {
          // Determine active session from the current page URL
          var activeId = getActiveSessionId();
          var url = '/api/session-tabs';
          if (activeId) {
            url += '?active=' + encodeURIComponent(activeId);
          }
          htmx.ajax('GET', url, { swap: 'none' });
        }
        break;

      case 'connected':
        // Initial connection established — no action needed
        break;

      default:
        break;
    }
  }

  function getActiveSessionId() {
    // Extract session ID from URL pattern /sessions/{id}
    var match = window.location.pathname.match(/^\/sessions\/([a-f0-9]+)/);
    return match ? match[1] : '';
  }

  // Initialize on page load
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', connect);
  } else {
    connect();
  }

  // Reconnect after HTMX swaps that replace the whole app (navigation)
  document.addEventListener('htmx:beforeSwap', function (evt) {
    var targetId = evt.detail && evt.detail.target && evt.detail.target.id;
    if (targetId === 'app') {
      // Full page swap — reconnect event stream
      if (reconnectTimer) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      reconnectDelay = 1000;
      connect();
    }
  });

  // Clean up on page unload
  window.addEventListener('beforeunload', function () {
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
  });
})();
