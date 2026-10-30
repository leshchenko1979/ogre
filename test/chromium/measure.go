//go:build chromium

// Package chromium measures Ogre's layout geometry against a real browser.
//
// Both engines render the same HTML to PNG, and both PNGs are measured with the
// same instrument: every probe colour in the palette is located and its
// bounding box reported. Because the ruler is identical on both sides, an
// instrument error cancels instead of masquerading as a renderer error.
//
// The suite is gated behind the "chromium" build tag, so `go test ./...` — and
// CI's default job — never needs a browser.
package chromium

import (
	"fmt"
	"image"
	"image/png"
	"io"
)

// Colour is one exact RGB probe colour. Fixtures paint elements with these so
// the instrument can find them without knowing anything about the document.
type Colour struct {
	Name    string
	R, G, B uint8
}

// Hex returns the CSS hex form of the colour.
func (c Colour) Hex() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Box is a pixel bounding box, half-open on the right and bottom edges: it
// covers columns X..X+W-1 and rows Y..Y+H-1.
type Box struct {
	X, Y, W, H int
}

func (b Box) String() string {
	return fmt.Sprintf("x=%d y=%d w=%d h=%d", b.X, b.Y, b.W, b.H)
}

// Delta returns the largest per-field difference between two boxes.
func (b Box) Delta(o Box) int {
	d := func(a, c int) int {
		if a > c {
			return a - c
		}
		return c - a
	}
	return max(d(b.X, o.X), d(b.Y, o.Y), d(b.W, o.W), d(b.H, o.H))
}

// Palette holds the probe colours. Every pair is at least 64 apart in at least
// one channel, so an antialiased edge of one can never be mistaken for another:
// the matcher's tolerance is an order of magnitude below that gap.
var Palette = []Colour{
	{"red", 255, 0, 0},
	{"green", 0, 255, 0},
	{"blue", 0, 0, 255},
	{"yellow", 255, 255, 0},
	{"magenta", 255, 0, 255},
	{"cyan", 0, 255, 255},
	{"orange", 255, 128, 0},
	{"black", 0, 0, 0},
}

// MatchTolerance is the largest per-channel distance still counted as a match.
// Filled rectangles land on integer coordinates in both engines, so their
// interiors are exact; the tolerance only absorbs premultiplied-alpha rounding
// on a half-covered edge pixel.
const MatchTolerance = 8

// Measure locates every palette colour in a PNG and returns its bounding box.
// Colours absent from the image are absent from the map.
//
// Pixels are composited over white first: Ogre writes premultiplied RGBA, so a
// transparent pixel reads as (0,0,0,0) and would otherwise register as black.
func Measure(r io.Reader, pal []Colour) (map[string]Box, error) {
	img, err := png.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decode png: %w", err)
	}
	return MeasureImage(img, pal), nil
}

// MeasureImage is Measure for an already decoded image.
func MeasureImage(img image.Image, pal []Colour) map[string]Box {
	type extent struct {
		minX, minY, maxX, maxY int
		found                  bool
	}
	ext := make([]extent, len(pal))

	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			// At() returns alpha-premultiplied values on a 0..0xffff scale, so
			// compositing over white is c + white*(1-a), already in that scale.
			over := func(c uint32) uint8 {
				v := (c + 0xffff - a) / 0x101
				if v > 255 {
					v = 255
				}
				return uint8(v)
			}
			pr, pg, pb := over(r), over(g), over(bl)

			for i := range pal {
				if !near(pr, pg, pb, pal[i]) {
					continue
				}
				e := &ext[i]
				if !e.found {
					*e = extent{minX: x, minY: y, maxX: x, maxY: y, found: true}
					continue
				}
				e.minX = min(e.minX, x)
				e.minY = min(e.minY, y)
				e.maxX = max(e.maxX, x)
				e.maxY = max(e.maxY, y)
			}
		}
	}

	out := make(map[string]Box, len(pal))
	for i, e := range ext {
		if !e.found {
			continue
		}
		out[pal[i].Name] = Box{
			X: e.minX,
			Y: e.minY,
			W: e.maxX - e.minX + 1,
			H: e.maxY - e.minY + 1,
		}
	}
	return out
}

func near(r, g, b uint8, c Colour) bool {
	return diff(r, c.R) <= MatchTolerance &&
		diff(g, c.G) <= MatchTolerance &&
		diff(b, c.B) <= MatchTolerance
}

func diff(a, b uint8) int {
	if a > b {
		return int(a) - int(b)
	}
	return int(b) - int(a)
}
