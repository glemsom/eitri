// eitri-session-id — Shared session-ID extraction for all browser islands.
// Session IDs are opaque strings that may contain hex characters as well as
// '-' and '_' (e.g. imported or restored sessions), so every island must
// derive the session ID from the URL through this single helper. Islands must
// NOT parse session IDs themselves — use window.eitriGetSessionId().
// (issue #1077)

(function () {
  'use strict';

  // Extract the session ID from a URL path. Defaults to the current page URL.
  // Matches both the session page (/sessions/{id}) and the chat form action
  // (/api/sessions/{id}/chat). Returns '' when no session ID is present.
  function eitriGetSessionId(url) {
    var path = url || window.location.pathname;
    var match = path.match(/\/(?:api\/)?sessions\/([a-zA-Z0-9_-]+)/);
    return match ? match[1] : '';
  }

  window.eitriGetSessionId = eitriGetSessionId;
})();
