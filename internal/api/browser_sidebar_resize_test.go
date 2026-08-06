//go:build e2e

package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// TestBrowser_SidebarResizeHandleFollowsWidth reproduces the "can only resize
// the sidebar once" bug: the drag sets --sidebar-width inline on #app, but the
// resize handle lives outside #app (appended to body), so the handle never
// inherits the new width and stays stranded at the original position after the
// first drag — a second drag at the sidebar edge does nothing.
func TestBrowser_SidebarResizeHandleFollowsWidth(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	if err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#sidebar", chromedp.ByQuery),
		chromedp.WaitVisible("#sidebar-resize-handle", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	sidebarWidth := func() int {
		var w int
		if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(
			`document.getElementById('sidebar').offsetWidth`, &w)); err != nil {
			t.Fatalf("read sidebar width: %v", err)
		}
		return w
	}
	handleLeft := func() int {
		var x float64
		if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(
			`document.getElementById('sidebar-resize-handle').getBoundingClientRect().x`, &x)); err != nil {
			t.Fatalf("read handle x: %v", err)
		}
		return int(x)
	}

	// Drag from the handle's current centre to +160px in 32px steps.
	drag := func(x1, x2 int) {
		const y = 400
		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			if err := input.DispatchMouseEvent(input.MousePressed, float64(x1), float64(y)).
				WithButton("left").WithButtons(1).WithClickCount(1).Do(ctx); err != nil {
				return err
			}
			for x := x1 + 32; x <= x2; x += 32 {
				if err := input.DispatchMouseEvent(input.MouseMoved, float64(x), float64(y)).
					WithButton("left").WithButtons(1).Do(ctx); err != nil {
					return err
				}
				time.Sleep(10 * time.Millisecond)
			}
			return input.DispatchMouseEvent(input.MouseReleased, float64(x2), float64(y)).
				WithButton("left").WithButtons(0).WithClickCount(1).Do(ctx)
		}))
		if err != nil {
			t.Fatalf("drag failed: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	initialWidth := sidebarWidth()
	initialHandle := handleLeft()

	// First drag: grow the sidebar.
	drag(initialHandle+3, initialHandle+163)
	widthAfterFirst := sidebarWidth()
	handleAfterFirst := handleLeft()
	if widthAfterFirst <= initialWidth {
		t.Fatalf("first drag did not resize sidebar: %d -> %d", initialWidth, widthAfterFirst)
	}

	// The handle must follow the sidebar edge: handle left edge sits at
	// width-3 (CSS: left: calc(var(--sidebar-width) - 3px)).
	if got, want := handleAfterFirst, widthAfterFirst-3; got < want-2 || got > want+2 {
		t.Fatalf("handle stranded after first drag: sidebar=%d handle_x=%d (want ~%d) — "+
			"the handle no longer tracks the sidebar, so the user cannot grab it again",
			widthAfterFirst, got, want)
	}

	// Second drag: grab at the sidebar edge (where the handle now is) and
	// grow again. This is the exact step that failed before the fix.
	edge := widthAfterFirst
	drag(edge, edge+100)
	widthAfterSecond := sidebarWidth()
	if widthAfterSecond <= widthAfterFirst {
		t.Fatalf("second resize did nothing: sidebar stayed at %d — "+
			"resize only works once", widthAfterFirst)
	}
}
