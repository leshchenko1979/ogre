//go:build chromium

package chromium

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/macawls/ogre/v3"
)

// Tolerance is the largest per-field difference, in pixels, that still counts
// as agreement. Filled rectangles land on integer coordinates in both engines;
// the slack exists for sub-pixel rounding of fractional sizes (an aspect ratio
// of 3:1 rarely divides evenly) and for edge-pixel coverage.
const Tolerance = 1

// Fixture is one geometry probe: a body fragment rendered at a fixed viewport
// by both engines, then measured.
type Fixture struct {
	Name string
	File string
	// Width and Height are the viewport both engines get. Keep them small:
	// the instrument is O(pixels x palette), and a fixture only needs room for
	// the boxes it probes.
	Width, Height int
	// Text is true when the fixture renders glyphs, which makes the harness
	// embed the parity font in both documents.
	Text bool
	// Known cites an open issue when Ogre is expected to disagree with the
	// browser. A known defect that starts matching fails the test: the marker
	// has been earned back and must be removed.
	Known string
}

// Fixtures is the corpus. Each one pins a behaviour that has either regressed
// before or is a plausible place for the two engines to diverge.
var Fixtures = []Fixture{
	{
		Name: "content-box-flex", File: "content-box-flex.html",
		Width: 400, Height: 100,
	},
	{
		Name: "border-box-flex", File: "border-box-flex.html",
		Width: 400, Height: 100,
	},
	{
		Name: "content-box-absolute", File: "content-box-absolute.html",
		Width: 400, Height: 200,
	},
	{
		Name: "content-box-aspect-ratio", File: "content-box-aspect-ratio.html",
		Width: 400, Height: 200,
	},
	{
		Name: "content-box-flex-basis", File: "content-box-flex-basis.html",
		Width: 400, Height: 100,
	},
	{
		Name: "br-run-blocks", File: "br-run-blocks.html",
		Width: 100, Height: 200,
	},
	{
		Name: "br-run-text", File: "br-run-text.html",
		Width: 200, Height: 200, Text: true,
	},
	{
		// Not a baseline defect: Ogre has no inline formatting context, so the
		// swatch and the text beside it land on separate block lines. The
		// fixture measures the symptom of that larger gap — see
		// https://github.com/macawls/ogre/issues/43#issuecomment-6528193680
		Name: "inline-block-baseline", File: "inline-block-baseline.html",
		Width: 200, Height: 100, Text: true,
		Known: "https://github.com/macawls/ogre/issues/43",
	},
	{
		Name: "absolute-auto-bottom", File: "absolute-auto-bottom.html",
		Width: 400, Height: 200,
		Known: "https://github.com/macawls/ogre/issues/40",
	},
	{
		Name: "flex-grow", File: "flex-grow.html",
		Width: 400, Height: 100,
	},
	{
		Name: "percentage-padding", File: "percentage-padding.html",
		Width: 400, Height: 100,
	},
	{
		Name: "grid-columns", File: "grid-columns.html",
		Width: 400, Height: 100,
	},
	{
		Name: "flex-align-center", File: "flex-align-center.html",
		Width: 400, Height: 200,
	},
	{
		Name: "margin-auto-center", File: "margin-auto-center.html",
		Width: 400, Height: 100,
	}, {
		Name: "margin-auto-stretch", File: "margin-auto-stretch.html",
		Width: 400, Height: 100,
	}, {
		Name: "margin-auto-left", File: "margin-auto-left.html",
		Width: 400, Height: 100,
	},
	{
		Name: "overflow-clip", File: "overflow-clip.html",
		Width: 200, Height: 100,
	},
	{
		Name: "flex-gap", File: "flex-gap.html",
		Width: 400, Height: 100,
	},
	{
		Name: "flex-justify-between", File: "flex-justify-between.html",
		Width: 400, Height: 100,
	},
	{
		Name: "absolute-explicit-corners", File: "absolute-explicit-corners.html",
		Width: 400, Height: 200,
	},
	{
		Name: "relative-offset", File: "relative-offset.html",
		Width: 400, Height: 200,
	},
	{
		Name: "relative-offset-subtree", File: "relative-offset-subtree.html",
		Width: 400, Height: 200,
	},
	{
		Name: "absolute-in-relative-parent", File: "absolute-in-relative-parent.html",
		Width: 400, Height: 200,
	},
	{
		Name: "percent-width", File: "percent-width.html",
		Width: 400, Height: 100,
	},
	{
		Name: "min-width-block", File: "min-width-block.html",
		Width: 400, Height: 100,
	},
	{
		Name: "min-max-width", File: "min-max-width.html",
		Width: 400, Height: 100,
	},
	{
		Name: "border-width", File: "border-width.html",
		Width: 400, Height: 100,
	},
}

// renderBoth renders one fixture through Chromium and through Ogre and returns
// the measurements from each.
func renderBoth(t *testing.T, b *Browser, dir string, f Fixture, body string) (chromium, ogreBoxes map[string]Box) {
	t.Helper()

	doc := document(body, f.Text)
	htmlPath := filepath.Join(dir, f.Name+".html")
	if err := os.WriteFile(htmlPath, []byte(doc), 0o644); err != nil {
		t.Fatalf("write fixture html: %v", err)
	}

	pngPath := filepath.Join(dir, f.Name+".chromium.png")
	if err := b.Screenshot(htmlPath, pngPath, f.Width, f.Height); err != nil {
		// A browser that never started says nothing about Ogre's layout. Say so
		// plainly instead of reporting a phantom geometry failure; CI sets
		// CHROMIUM_REQUIRED, where this is fatal rather than skipped.
		if errors.Is(err, ErrNoScreenshot) {
			skipOrFail(t, "chromium could not render the fixture: %v", err)
		}
		t.Fatalf("chromium screenshot: %v", err)
	}
	cf, err := os.Open(pngPath)
	if err != nil {
		t.Fatalf("open chromium png: %v", err)
	}
	defer cf.Close()
	chromium, err = Measure(cf, Palette)
	if err != nil {
		t.Fatalf("measure chromium png: %v", err)
	}

	var fonts []ogre.FontSource
	if f.Text {
		fonts = []ogre.FontSource{GoFont()}
	}
	ogrePNG, err := RenderOgre(doc, f.Width, f.Height, fonts)
	if err != nil {
		t.Fatalf("ogre render: %v", err)
	}
	ogreBoxes, err = Measure(strings.NewReader(string(ogrePNG)), Palette)
	if err != nil {
		t.Fatalf("measure ogre png: %v", err)
	}
	return chromium, ogreBoxes
}

// compare reports every probe colour whose box disagrees by more than tol.
func compare(chromium, ogre map[string]Box, tol int) (diffs []string, worst int) {
	names := map[string]bool{}
	for n := range chromium {
		names[n] = true
	}
	for n := range ogre {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	for _, n := range sorted {
		c, okC := chromium[n]
		o, okO := ogre[n]
		switch {
		case !okO:
			diffs = append(diffs, fmt.Sprintf("%s: missing from Ogre (chromium has %s)", n, c))
		case !okC:
			diffs = append(diffs, fmt.Sprintf("%s: missing from Chromium (ogre has %s)", n, o))
		default:
			if d := c.Delta(o); d > tol {
				diffs = append(diffs, fmt.Sprintf("%s: chromium %s, ogre %s (delta %dpx)", n, c, o, d))
				worst = max(worst, d)
			}
		}
	}
	return diffs, worst
}

// TestChromiumParity is the gate: every fixture is rendered by both engines and
// their geometry must agree.
func TestChromiumParity(t *testing.T) {
	b := resolveOrSkip(t)
	dir := t.TempDir()

	var rows []string
	var failed, known int

	for _, f := range Fixtures {
		f := f
		t.Run(f.Name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("fixtures", f.File))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			cr, og := renderBoth(t, b, dir, f, string(body))
			diffs, _ := compare(cr, og, Tolerance)

			status := "PASS"
			switch {
			case len(diffs) == 0 && f.Known != "":
				status = "FIXED?"
			case len(diffs) > 0 && f.Known != "":
				status = "known"
			case len(diffs) > 0:
				status = "FAIL"
			}

			for _, name := range probeNames(cr, og) {
				cb, okC := cr[name]
				ob, okO := og[name]
				rows = append(rows, fmt.Sprintf("%-24s %-8s %-24s %-24s %s",
					f.Name, name, boxOrDash(cb, okC), boxOrDash(ob, okO), verdict(cb, okC, ob, okO)))
			}

			switch status {
			case "FAIL":
				failed++
				for _, d := range diffs {
					t.Errorf("%s", d)
				}
			case "FIXED?":
				failed++
				t.Errorf("marked as a known defect (%s) but now matches Chromium: remove the marker from Fixtures", f.Known)
			case "known":
				known++
				t.Logf("known defect %s still open: %s", f.Known, strings.Join(diffs, "; "))
			}
		})
	}

	t.Logf("\n%s\n%-24s %-8s %-24s %-24s %s\n%s",
		strings.Repeat("-", 100), "fixture", "colour", "chromium", "ogre", "verdict", strings.Repeat("-", 100))
	for _, r := range rows {
		t.Logf("%s", r)
	}
	t.Logf("%d fixtures, %d failed, %d known defects", len(Fixtures), failed, known)
}

func probeNames(cr, og map[string]Box) []string {
	seen := map[string]bool{}
	for n := range cr {
		seen[n] = true
	}
	for n := range og {
		seen[n] = true
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func boxOrDash(b Box, present bool) string {
	if !present {
		return "-"
	}
	return b.String()
}

func verdict(cb Box, okC bool, ob Box, okO bool) string {
	switch {
	case !okC:
		return "ONLY OGRE"
	case !okO:
		return "MISSING"
	case cb.Delta(ob) > Tolerance:
		return fmt.Sprintf("DIFF %dpx", cb.Delta(ob))
	default:
		return "ok"
	}
}
