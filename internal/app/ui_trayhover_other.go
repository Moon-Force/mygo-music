//go:build !windows

package app

// trayPromoted is Windows' overflow-area visibility; elsewhere the hover
// panel is not used, and nothing hides the icon, so it always reports true.
var trayPromoted = func() bool { return true }

// hoverWatch is the shell's reports of the pointer over an icon, which
// only Windows' notification area gives.
type hoverWatch struct{}

func (h *trayHover) listen()   {}
func (h *trayHover) unlisten() { h.hoverOn = false }
