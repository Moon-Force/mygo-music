package app

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A notch of a mouse wheel turns the volume a fixed step however far the
// system scrolls a notch; a touchpad turns it as far as it scrolls.
func TestVolumeScroll(t *testing.T) {
	for _, c := range []struct {
		ev   ui.InputEvent
		want float64
	}{
		{ui.InputEvent{Kind: ui.InputScroll, DY: 99}, wheelVolumeStep},
		{ui.InputEvent{Kind: ui.InputScroll, DY: 132}, wheelVolumeStep},
		{ui.InputEvent{Kind: ui.InputScroll, DY: -132}, -wheelVolumeStep},
		{ui.InputEvent{Kind: ui.InputScroll, DX: 40}, 0},
		{ui.InputEvent{Kind: ui.InputScroll, DY: 40, Precise: true}, 0.1},
	} {
		if got := volumeScroll(c.ev); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%+v turns the volume down %v, want %v", c.ev, got, c.want)
		}
	}
}
