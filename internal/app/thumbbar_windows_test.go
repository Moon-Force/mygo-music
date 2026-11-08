//go:build windows

package app

import (
	"testing"
	"unsafe"
)

// The shell reads THUMBBUTTON in C's layout: a field moved or retyped here
// would hand it garbage, so the layout is pinned.
func TestThumbButtonLayout(t *testing.T) {
	var b thumbButton
	if unsafe.Sizeof(b) != 552 {
		t.Errorf("THUMBBUTTON is %d bytes, want 552", unsafe.Sizeof(b))
	}
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Icon", unsafe.Offsetof(b.Icon), 16},
		{"Tip", unsafe.Offsetof(b.Tip), 24},
		{"Flags", unsafe.Offsetof(b.Flags), 544},
	} {
		if c.got != c.want {
			t.Errorf("%s at %d, want %d", c.name, c.got, c.want)
		}
	}
}

// Turning the setting off hides the three buttons (they cannot be removed),
// and the middle one wears the pause icon while the music plays.
func TestThumbButtonsHideAndPlayIcon(t *testing.T) {
	tb := &thumbbar{icons: [4]uintptr{11, 12, 13, 14}}
	for _, c := range []struct {
		enabled, playing bool
		flags            uint32
		play             uintptr
	}{
		{true, false, thbfEnabled, 12},
		{true, true, thbfEnabled, 13},
		{false, false, thbfHidden, 12},
		{false, true, thbfHidden, 13},
	} {
		tb.enabled, tb.playing = c.enabled, c.playing
		bs := tb.buttons()
		for i, b := range bs {
			if b.Flags != c.flags {
				t.Errorf("enabled=%v: button %d flags %#x, want %#x", c.enabled, i, b.Flags, c.flags)
			}
		}
		if bs[0].Icon != 11 || bs[2].Icon != 14 {
			t.Errorf("prev/next icons %d/%d, want 11/14", bs[0].Icon, bs[2].Icon)
		}
		if bs[1].Icon != c.play {
			t.Errorf("playing=%v: middle icon %d, want %d", c.playing, bs[1].Icon, c.play)
		}
	}
}
