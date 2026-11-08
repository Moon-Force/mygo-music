package app

import (
	"bytes"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
	"mygo-music/internal/player"
)

// trayHoverFor makes a controller whose icon sits at a fixed place, as the
// tests' pointer goes where they say, and shows in the corner rather than
// hidden in the overflow.
func trayHoverFor(t *testing.T, a *app, pt *mygo.Point) *trayHover {
	t.Helper()
	h := &trayHover{a: a, bounds: func() mygo.Rectangle {
		return mygo.Rectangle{X: 1000, Y: 900, Width: 24, Height: 24}
	}}
	realCursor := cursorPoint
	cursorPoint = func() mygo.Point { return *pt }
	realPromoted := trayPromoted
	trayPromoted = func() bool { return true }
	t.Cleanup(func() {
		cursorPoint = realCursor
		trayPromoted = realPromoted
	})
	return h
}

// The pointer passing over the tray's icon does not open the panel: one
// that rests there does, and leaving takes it away after a grace.
func TestTrayHoverRestOpensAndLeaveCloses(t *testing.T) {
	a := newTestApp()
	pt := mygo.Point{X: 1012, Y: 912}
	h := trayHoverFor(t, a, &pt)
	defer h.stop()

	h.poll()
	if h.open {
		t.Fatal("a pointer just arrived opened the panel")
	}
	h.enteredAt = h.lastInside.Add(-time.Second) // as if it had rested there
	h.poll()
	if !h.open {
		t.Fatal("the pointer resting on the icon did not open the panel")
	}

	pt = mygo.Point{X: 500, Y: 400}
	h.poll()
	if !h.open {
		t.Fatal("the panel closed the moment the pointer left")
	}
	h.lastInside = time.Now().Add(-time.Second)
	h.poll()
	if h.open {
		t.Fatal("the panel stayed after the pointer left for good")
	}
}

// The pointer moving from the icon into the panel keeps it open: the
// corridor between them counts as over it.
func TestTrayHoverStaysOverPanel(t *testing.T) {
	a := newTestApp()
	pt := mygo.Point{X: 1012, Y: 912}
	h := trayHoverFor(t, a, &pt)
	defer h.stop()
	h.panelBounds = func() mygo.Rectangle {
		return mygo.Rectangle{X: 900, Y: 780, Width: trayHoverW, Height: trayHoverH}
	}

	h.poll()
	h.enteredAt = h.lastInside.Add(-time.Second)
	h.poll()
	if !h.open {
		t.Fatal("did not open")
	}
	pt = mygo.Point{X: 1000, Y: 830} // in the panel, off the icon
	h.poll()
	if !h.open {
		t.Fatal("closed while the pointer was in the panel")
	}
	pt = mygo.Point{X: 1012, Y: 895} // the gap between panel and icon
	h.poll()
	if !h.open {
		t.Fatal("closed while the pointer crossed to the icon")
	}
}

// Turned off, the panel does not open however long the pointer rests; an
// icon whose place the shell does not report opens nothing either.
func TestTrayHoverOffOrHidden(t *testing.T) {
	a := newTestApp()
	pt := mygo.Point{X: 1012, Y: 912}
	h := trayHoverFor(t, a, &pt)
	defer h.stop()

	a.settings.TrayHover = false
	h.poll()
	h.enteredAt = time.Now().Add(-time.Second)
	h.poll()
	if h.open {
		t.Fatal("opened while turned off")
	}

	a.settings.TrayHover = true
	h.bounds = func() mygo.Rectangle { return mygo.Rectangle{} }
	h.poll()
	h.enteredAt = time.Now().Add(-time.Second)
	h.poll()
	if h.open {
		t.Fatal("opened over an icon whose place is unknown")
	}
}

// An icon hidden in the overflow area reports the chevron's rectangle, so
// resting on the chevron opened the panel by mistake: only the shell's
// report of the pointer over the icon itself, in the overflow area's
// flyout, may open it, and leaving closes it once more.
func TestTrayHoverHiddenInOverflow(t *testing.T) {
	a := newTestApp()
	pt := mygo.Point{X: 1012, Y: 912} // the chevron's rectangle, as reported
	h := trayHoverFor(t, a, &pt)
	defer h.stop()
	trayPromoted = func() bool { return false } // hidden behind the chevron

	h.poll()
	h.enteredAt = time.Now().Add(-time.Second) // as if it had rested there
	h.poll()
	if h.open {
		t.Fatal("opened over the chevron rather than the icon")
	}

	icon := mygo.Point{X: 980, Y: 860} // in the overflow area's flyout
	pt = icon
	h.hovered(icon)
	h.enteredAt = time.Now().Add(-time.Second) // as if it had rested there
	h.poll()
	if !h.open {
		t.Fatal("did not open over the icon in the overflow area")
	}

	pt = mygo.Point{X: 500, Y: 400}
	h.poll()
	h.lastInside = time.Now().Add(-time.Second)
	h.poll()
	if h.open {
		t.Fatal("stayed after the pointer left the icon in the overflow area")
	}

	// The report is spent once the pointer has gone: that place is not
	// the icon's any more.
	pt = icon
	h.poll()
	h.enteredAt = time.Now().Add(-time.Second)
	h.poll()
	if h.open {
		t.Fatal("opened over a place the shell no longer reports")
	}
}

// silenceWAV is a second of silence as a WAV stream, for the panel's
// controls to play without a file.
func silenceWAV() []byte {
	var b bytes.Buffer
	const frames = 48000
	data := frames * 4
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&b, binary.LittleEndian, uint16(2)) // channels
	binary.Write(&b, binary.LittleEndian, uint32(48000))
	binary.Write(&b, binary.LittleEndian, uint32(48000*4))
	binary.Write(&b, binary.LittleEndian, uint16(4))
	binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(data))
	b.Write(make([]byte, data))
	return b.Bytes()
}

// The panel's buttons play: next and previous go through the queue, and
// the middle one pauses and plays again.
func TestTrayHoverControls(t *testing.T) {
	wav := silenceWAV()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(wav)
	}))
	defer srv.Close()

	a := newTestApp()
	ch := posts(a)
	a.pl = player.New(a.post)
	a.pl.Source = a.sourceByID
	a.pl.SetVolume(0)
	fake(a).stream = func(id, quality string) (*music.Stream, error) {
		return &music.Stream{URL: srv.URL, Type: "wav"}, nil
	}
	tracks := []music.Track{song("1", "第一首"), song("2", "第二首")}
	for i := range tracks {
		tracks[i].Source = "fake"
	}
	a.pl.PlayQueue(tracks, 0)
	until(t, ch, func() bool { return a.pl.Current() != nil && a.pl.Current().ID == "1" && a.pl.Playing() })

	h := &trayHover{a: a}
	tt := ui.NewTester(h.view, trayHoverW, trayHoverH)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()

	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	until(t, ch, func() bool { return a.pl.Current() != nil && a.pl.Current().ID == "2" })
	if err := tt.Click("Previous"); err != nil {
		t.Fatal(err)
	}
	until(t, ch, func() bool { return a.pl.Current() != nil && a.pl.Current().ID == "1" })

	if err := tt.Click("Play/Pause"); err != nil {
		t.Fatal(err)
	}
	if a.pl.Playing() {
		t.Error("the middle button did not pause")
	}
	tt.Frame()
	if err := tt.Click("Play/Pause"); err != nil {
		t.Fatal(err)
	}
	if !a.pl.Playing() {
		t.Error("the middle button did not play again")
	}
}
