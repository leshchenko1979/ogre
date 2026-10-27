package ogre

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// inkBandTops returns the first row of every horizontal band of ink, top to
// bottom. Used to measure line pitch without depending on glyph shapes.
func inkBandTops(img image.Image) []int {
	bounds := img.Bounds()
	isInk := make([]bool, bounds.Max.Y)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			// Composite over white: Ogre writes premultiplied RGBA, so a fully
			// transparent pixel reads as (0,0,0,0) and would count as ink.
			r, g, b, a := img.At(x, y).RGBA()
			over := func(c uint32) uint32 { return (c*255 + 255*(0xffff-a)) / 0xffff }
			if over(r) < 250 || over(g) < 250 || over(b) < 250 {
				isInk[y] = true
				break
			}
		}
	}

	var tops []int
	inBand := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if isInk[y] && !inBand {
			tops = append(tops, y)
			inBand = true
		} else if !isInk[y] {
			inBand = false
		}
	}
	return tops
}

// A <br> must advance the baseline by exactly one line box. It used to advance
// by two: the break became its own text leaf, which took a full line box on top
// of the one the following text already occupied.
func TestRenderBrAdvancesOneLineBox(t *testing.T) {
	measure := func(html string) []int {
		result, err := Render(html, Options{Width: 600, Height: 300, Format: FormatPNG})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		img, err := png.Decode(bytes.NewReader(result.Data))
		if err != nil {
			t.Fatalf("decode png: %v", err)
		}
		return inkBandTops(img)
	}

	style := `padding:20px;font-size:20px;line-height:1.15;color:#000;`

	// Control: the same three lines separated by block boxes, which already
	// advanced one line box each.
	blockTops := measure(`<body style="margin:0;padding:0;width:600px;height:300px;background:#fff;">` +
		`<div style="` + style + `"><span>Первая</span><span style="display:block">Вторая</span>` +
		`<span style="display:block">Третья</span></div></body>`)

	brTops := measure(`<body style="margin:0;padding:0;width:600px;height:300px;background:#fff;">` +
		`<div style="` + style + `">Первая<br>Вторая<br>Третья</div></body>`)

	if len(blockTops) != 3 {
		t.Fatalf("control produced %d ink bands, want 3: %v", len(blockTops), blockTops)
	}
	if len(brTops) != 3 {
		t.Fatalf("three lines separated by <br> produced %d ink bands, want 3: %v", len(brTops), brTops)
	}

	for i := 1; i < 3; i++ {
		gotStep := brTops[i] - brTops[i-1]
		wantStep := blockTops[i] - blockTops[i-1]
		if gotStep != wantStep {
			t.Fatalf("<br> line %d starts %d px after the previous line, want %d (one line box): %v",
				i+1, gotStep, wantStep, brTops)
		}
	}
}
