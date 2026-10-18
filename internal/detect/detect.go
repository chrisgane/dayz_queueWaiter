// Package detect works out which DayZ screen is showing from a screenshot of
// the monitor the game is on.
//
// It doesn't read any text. It looks at a few spots of the screen whose
// colour gives each screen away, measured from screenshots of the real game:
//
//   - The server browser has a pure black bar above the server list (the
//     "Server / Map / Players / Ping" header) and another below it ("Servers
//     found"). Both stay visible while a popup is open.
//   - The "CONNECTING FAILED" popup is a grey panel in the middle of the
//     screen with a red OK button. The "Loading in …" / queue screen has a
//     similar red button, but a little lower and without the server browser
//     behind it.
//
// Positions are fractions of the screen size, so any 16:9 resolution works.
package detect

// Frame is a screenshot of the monitor DayZ is on.
type Frame interface {
	Size() (w, h int)
	RGB(x, y int) (r, g, b uint8)
}

// State is what the game is showing.
type State int

const (
	// Away: not the server browser. Connecting, in the queue, loading, or
	// anything else.
	Away State = iota
	// Browser: the server browser with no popup.
	Browser
	// PopupFull: the "Maximum number of players has been reached." popup.
	PopupFull
	// PopupOther: some other popup over the server browser.
	PopupOther
)

func (s State) String() string {
	switch s {
	case Browser:
		return "server browser"
	case PopupFull:
		return "server full popup"
	case PopupOther:
		return "other popup"
	}
	return "not the server browser"
}

// IsPopup reports whether s is a popup over the server browser.
func (s State) IsPopup() bool { return s == PopupFull || s == PopupOther }

// Box is an area of the screen, as fractions of its width and height.
type Box struct{ X0, Y0, X1, Y1 float64 }

var (
	headerBar = Box{0.30, 0.153, 0.45, 0.178}
	footerBar = Box{0.10, 0.858, 0.50, 0.883}

	// The top part of the OK button and the panel either side of it. The
	// loading screen's Leave button starts just below this strip.
	okButton   = Box{0.46, 0.5396, 0.545, 0.5502}
	panelLeft  = Box{0.38, 0.5396, 0.44, 0.5502}
	panelRight = Box{0.565, 0.5396, 0.625, 0.5502}

	// The popup's message line. "Maximum number of players has been
	// reached." is about 23% of the screen wide.
	message         = Box{0.36, 0.490, 0.64, 0.517}
	fullMessageWide = 0.17

	// ServerList is where the server rows are.
	ServerList = Box{0.042, 0.186, 0.671, 0.851}
)

// Classify works out which screen f shows.
func Classify(f Frame) State {
	if fraction(f, headerBar, isBlack) < 0.9 || fraction(f, footerBar, isBlack) < 0.9 {
		return Away
	}
	if fraction(f, okButton, isRed) >= 0.6 &&
		fraction(f, panelLeft, isPanel) >= 0.8 &&
		fraction(f, panelRight, isPanel) >= 0.8 {
		if textWidth(f, message) >= fullMessageWide {
			return PopupFull
		}
		return PopupOther
	}
	return Browser
}

// Contains reports whether the pixel (x, y) of a w×h screen is inside b.
func (b Box) Contains(w, h, x, y int) bool {
	fx, fy := float64(x)/float64(w), float64(y)/float64(h)
	return fx >= b.X0 && fx <= b.X1 && fy >= b.Y0 && fy <= b.Y1
}

// Brightness is the average brightness of f from 0 to 255, sampled on a
// coarse grid. A black screenshot means screen capture isn't working.
func Brightness(f Frame) float64 {
	w, h := f.Size()
	var sum, n float64
	for y := h / 40; y < h; y += h / 20 {
		for x := w / 40; x < w; x += w / 20 {
			r, g, b := f.RGB(x, y)
			sum += (float64(r) + float64(g) + float64(b)) / 3
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

func pixels(f Frame, b Box) (x0, y0, x1, y1 int) {
	w, h := f.Size()
	x0, x1 = int(b.X0*float64(w)), int(b.X1*float64(w))
	y0, y1 = int(b.Y0*float64(h)), int(b.Y1*float64(h))
	if y1 <= y0 {
		y1 = y0 + 1
	}
	return x0, y0, x1, y1
}

// fraction is the share of pixels in b that match.
func fraction(f Frame, b Box, match func(r, g, b uint8) bool) float64 {
	x0, y0, x1, y1 := pixels(f, b)
	var n, hit int
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			n++
			if match(f.RGB(x, y)) {
				hit++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return float64(hit) / float64(n)
}

// textWidth is the width of the white text in b, as a fraction of the
// screen width.
func textWidth(f Frame, b Box) float64 {
	w, _ := f.Size()
	x0, y0, x1, y1 := pixels(f, b)
	first, last := -1, -1
	for x := x0; x < x1; x++ {
		for y := y0; y < y1; y++ {
			if isWhite(f.RGB(x, y)) {
				if first < 0 {
					first = x
				}
				last = x
				break
			}
		}
	}
	if first < 0 {
		return 0
	}
	return float64(last-first+1) / float64(w)
}

func isBlack(r, g, b uint8) bool { return max(r, g, b) <= 24 }

func isRed(r, g, b uint8) bool { return r >= 150 && g <= 70 && b <= 70 }

// isPanel matches the popup's flat dark grey.
func isPanel(r, g, b uint8) bool {
	hi, lo := max(r, g, b), min(r, g, b)
	return hi <= 70 && hi-lo <= 16
}

func isWhite(r, g, b uint8) bool { return min(r, g, b) >= 170 }

// BGRA is a screenshot in the 32-bit BGRA layout Windows uses.
type BGRA struct {
	W, H   int
	Stride int
	Pix    []byte
}

func (f *BGRA) Size() (w, h int) { return f.W, f.H }

func (f *BGRA) RGB(x, y int) (r, g, b uint8) {
	i := y*f.Stride + x*4
	return f.Pix[i+2], f.Pix[i+1], f.Pix[i]
}
