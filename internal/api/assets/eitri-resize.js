// Sidebar resize handle — drag to resize sidebar width
(function () {
  "use strict";

  let handle = null;
  let app = null;
  let startX = 0;
  let startWidth = 0;

  function init() {
    app = document.getElementById("app");
    if (!app) return;

    // Remove stale handle if any (e.g., after HTMX swap)
    const old = document.getElementById("sidebar-resize-handle");
    if (old) old.remove();

    handle = document.createElement("div");
    handle.id = "sidebar-resize-handle";
    handle.setAttribute("title", "Drag to resize sidebar");
    document.body.appendChild(handle);

    handle.addEventListener("mousedown", onMouseDown);
  }

  function onMouseDown(e) {
    e.preventDefault();
    startX = e.clientX;
    // Read current CSS variable or fallback to computed width of sidebar
    const computed = window.getComputedStyle(app).getPropertyValue("--sidebar-width").trim();
    const sidebarEl = document.getElementById("sidebar");
    startWidth = sidebarEl ? sidebarEl.offsetWidth : parseInt(computed || "240", 10);

    app.classList.add("resizing");
    handle.classList.add("dragging");

    document.addEventListener("mousemove", onMouseMove);
    document.addEventListener("mouseup", onMouseUp);
  }

  function onMouseMove(e) {
    const delta = e.clientX - startX;
    let newWidth = startWidth + delta;
    // Clamp to min/max
    newWidth = Math.max(120, Math.min(600, newWidth));
    app.style.setProperty("--sidebar-width", newWidth + "px");
  }

  function onMouseUp() {
    app.classList.remove("resizing");
    if (handle) handle.classList.remove("dragging");
    document.removeEventListener("mousemove", onMouseMove);
    document.removeEventListener("mouseup", onMouseUp);
  }

  // Init on page load and after HTMX swaps
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
  document.addEventListener("htmx:afterSwap", init);
})();
