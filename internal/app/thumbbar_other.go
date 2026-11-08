//go:build !windows

package app

// thumbbar is Windows' taskbar preview controls; elsewhere there are none.
type thumbbar struct{}

func newThumbbar(*app) *thumbbar    { return nil }
func (t *thumbbar) setEnabled(bool) {}
func (t *thumbbar) sync()           {}
func (t *thumbbar) destroy()        {}
