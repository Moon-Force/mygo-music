package app

import (
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// trayHover is the small player over the notification area's icon on
// Windows: it shows as the pointer rests on the icon, and goes as the
// pointer leaves both. The tray tells no hover, so the pointer is polled,
// as the notch's is.
type trayHover struct {
	a      *app
	panel  *mygo.Window // the panel, while it shows
	open   bool
	bounds func() mygo.Rectangle // the icon's, injectable in tests
	// panelBounds is the panel's screen rectangle, injectable in tests
	// where no window is made.
	panelBounds func() mygo.Rectangle

	enteredAt  time.Time
	lastInside time.Time
	polling    bool // a poll is due
	stopped    bool

	promoAt  time.Time // when the icon's visibility was last read
	promoVal bool      // whether it shows in the corner, not the overflow

	// hoverPt is where the pointer was when the shell last said it moved
	// over the icon, and hoverOn that it said so: an icon hidden behind
	// the chevron reports the chevron's rectangle, so this is what tells
	// where it really is.
	hoverPt mygo.Point
	hoverOn bool
	watch   *hoverWatch // the shell's reports of the pointer, on Windows

	offClick, offRight func() // the tray's listeners, dismissed by a click
}

const (
	trayHoverW     = 240
	trayHoverH     = 96
	trayHoverIcon  = 24                     // an icon in the overflow area, in DIPs
	trayHoverRest  = 150 * time.Millisecond // the pointer rests before it opens
	trayHoverGrace = 100 * time.Millisecond // it may leave before it closes
)

// newTrayHover makes the controller of a tray's icon; syncTrayHover starts
// it as the settings say.
func newTrayHover(a *app) *trayHover {
	h := &trayHover{a: a, bounds: a.tray.Bounds}
	h.offClick = a.tray.OnClick(h.dismiss)
	h.offRight = a.tray.OnRightClick(h.dismiss)
	return h
}

// syncTrayHover starts or stops the hover panel as the settings and the
// tray say.
func (a *app) syncTrayHover() {
	if runtime.GOOS != "windows" || a.tray == nil {
		return
	}
	if a.trayHover == nil {
		a.trayHover = newTrayHover(a)
	}
	if a.settings.TrayHover {
		a.trayHover.start()
	} else {
		a.trayHover.stop()
	}
}

func (h *trayHover) start() {
	if h.stopped {
		h.stopped = false
		h.offClick = h.a.tray.OnClick(h.dismiss)
		h.offRight = h.a.tray.OnRightClick(h.dismiss)
	}
	h.listen()
	if !h.polling {
		h.poll()
	}
}

// stop ends the polling and closes the panel; a poll due lands on the
// stopped flag and goes no further.
func (h *trayHover) stop() {
	if h.stopped {
		return
	}
	h.stopped = true
	h.unlisten()
	h.dismiss()
	h.enteredAt, h.lastInside = time.Time{}, time.Time{}
	if h.offClick != nil {
		h.offClick()
		h.offRight()
		h.offClick, h.offRight = nil, nil
	}
}

// dismiss closes the panel, as a click on the icon does: the main window
// or the menu takes over from it.
func (h *trayHover) dismiss() {
	if !h.open {
		return
	}
	h.open = false
	if h.panel != nil {
		h.panel.Close()
	}
}

// hovered records the shell's report that the pointer moved over the
// icon, wherever it shows: in the taskbar's corner, or in the overflow
// area's flyout, whose icons the corner's rectangle does not tell.
func (h *trayHover) hovered(pt mygo.Point) {
	if h.stopped || !h.a.settings.TrayHover {
		return
	}
	h.hoverPt, h.hoverOn = pt, true
	if !h.open {
		h.poll()
	}
}

// left records that the pointer left the icon, as the shell says.
func (h *trayHover) left() {
	h.hoverOn = false
}

// poll opens the panel under a pointer that rests on the icon, and closes
// it once the pointer has left the icon and the panel both.
func (h *trayHover) poll() {
	if h.stopped || !h.a.settings.TrayHover {
		return
	}
	icon, hasIcon := h.iconArea()
	area, known := icon, hasIcon
	if h.open {
		if panel, hasPanel := h.panelArea(); hasPanel {
			if hasIcon {
				area = unionRect(icon, panel)
			} else {
				area = panel
			}
			known = true
		}
	}
	inside := known && inRect(area, cursorPoint())
	now := time.Now()
	if inside {
		if h.lastInside.IsZero() || now.Sub(h.lastInside) > 100*time.Millisecond {
			h.enteredAt = now
		}
		h.lastInside = now
		// A pointer passing over the icon does not open it: one that
		// rests there does.
		if !h.open && now.Sub(h.enteredAt) >= trayHoverRest {
			h.openPanel()
		}
	} else {
		h.enteredAt = time.Time{}
		h.hoverOn = false // the report is spent once the pointer is off the icon
		if h.open && !h.lastInside.IsZero() && now.Sub(h.lastInside) > trayHoverGrace {
			h.dismiss()
		}
	}
	h.pollAgain(inside)
}

// iconArea is the box the pointer rests on to open the panel: the icon's
// rectangle where it shows in the taskbar's corner, and where the shell
// last reported a move over it where it hides behind the chevron, which
// reports the chevron's own rectangle instead.
func (h *trayHover) iconArea() (mygo.Rectangle, bool) {
	if r := h.bounds(); r.Width > 0 && r.Height > 0 && h.iconVisible() {
		return r, true
	}
	if h.hoverOn {
		return mygo.Rectangle{
			X:      h.hoverPt.X - trayHoverIcon/2,
			Y:      h.hoverPt.Y - trayHoverIcon/2,
			Width:  trayHoverIcon,
			Height: trayHoverIcon,
		}, true
	}
	return mygo.Rectangle{}, false
}

// panelArea is the panel's rectangle, where it shows.
func (h *trayHover) panelArea() (mygo.Rectangle, bool) {
	switch {
	case h.panelBounds != nil:
		r := h.panelBounds()
		return r, r.Width > 0 && r.Height > 0
	case h.panel != nil:
		return h.panel.Bounds(), true
	}
	return mygo.Rectangle{}, false
}

// inRect reports whether pt is within r, with some slack around it.
func inRect(r mygo.Rectangle, pt mygo.Point) bool {
	const slack = 6
	return pt.X >= r.X-slack && pt.X <= r.X+r.Width+slack &&
		pt.Y >= r.Y-slack && pt.Y <= r.Y+r.Height+slack
}

// iconVisible reports whether the tray's icon shows in the taskbar corner
// rather than hidden in the overflow area. The visibility changes rarely,
// so it is read at most once a second rather than on every poll. Off
// Windows the panel is not used and nothing hides the icon.
func (h *trayHover) iconVisible() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	now := time.Now()
	if h.promoAt.IsZero() || now.Sub(h.promoAt) > time.Second {
		h.promoVal = trayPromoted()
		h.promoAt = now
	}
	return h.promoVal
}

// pollAgain polls soon while the pointer is over the icon or the panel
// shows, for the times it rests and leaves; more slowly while the music
// plays or a window shows; and slowlyest at rest, to cost little.
func (h *trayHover) pollAgain(inside bool) {
	if h.stopped || h.polling {
		return
	}
	d := 250 * time.Millisecond
	switch {
	case inside || h.open:
		d = 33 * time.Millisecond
	case h.a.pl.Playing() || (h.a.win != nil && h.a.win.IsVisible()):
		d = 100 * time.Millisecond
	}
	h.polling = true
	time.AfterFunc(d, func() {
		mygo.RunOnMain(func() {
			h.polling = false
			h.poll()
		})
	})
}

// openPanel makes the panel anew each time: a flyout is placed against the
// icon as it is created, and the icon may have moved since the last.
func (h *trayHover) openPanel() {
	h.open = true
	if h.a.win == nil {
		return // as in tests
	}
	h.panel = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      h.a.tray,
		Placement: mygo.PlacementBottom,
		Gap:       8,
		Width:     trayHoverW,
		Height:    trayHoverH,
		Shadow:    true,
		Content:   ui.View(h.view),
	})
	h.panel.OnClosed(func() { h.panel = nil })
}

// redraw refreshes the panel, as the player's state changes.
func (h *trayHover) redraw() {
	if h.panel != nil {
		h.panel.Invalidate()
	}
}

// view is the panel: the song's name over its three controls.
func (h *trayHover) view(c *ui.Context) {
	a := h.a
	pl := a.pl
	t := c.Theme()
	name := T("Not Playing")
	if tr := pl.Current(); tr != nil {
		name = tr.Name
	}
	box := ui.Column(c).Fill()
	if runtime.GOOS == "windows" {
		// The system rounds and outlines the flyout there.
		box.Background(t.Surface)
	} else {
		box.Background(t.Surface).Radius(10).Border(1, t.Border)
	}
	box.Padding(12).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, name).FontSize(13).FontWeight(600).TextColor(t.Text).SingleLine()
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if iconButton(c.Key("prev"), iconPrev, 18, t.Text, T("Previous")).Clicked() {
				pl.Prev()
			}
			if playButton(c.Key("play"), pl.Playing(), 24, t.Text).Clicked() {
				pl.TogglePlay()
			}
			if iconButton(c.Key("next"), iconNext, 18, t.Text, T("Next")).Clicked() {
				pl.Next()
			}
		})
	})
}

// unionRect is the box around two rectangles, the corridor between the
// icon and its panel included.
func unionRect(a, b mygo.Rectangle) mygo.Rectangle {
	x := min(a.X, b.X)
	y := min(a.Y, b.Y)
	r := max(a.X+a.Width, b.X+b.Width)
	bottom := max(a.Y+a.Height, b.Y+b.Height)
	return mygo.Rectangle{X: x, Y: y, Width: r - x, Height: bottom - y}
}
