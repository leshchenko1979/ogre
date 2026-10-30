# Chromium geometry parity suite

Ogre's other suites cannot see a layout defect.

- **Unit tests** assert what the code computes, so they agree with the code by
  construction.
- **E2E pixel tests** (`ogre_*_e2e_test.go`) compare PNGs, and a pixel diff is
  blind to displacement: a 100×100 box moved 14px changes 2,800 of 756,000
  pixels and reads **99.63% identical**.
- **Visual regression** (`test/fixtures`) compares against **Satori** — the
  engine Ogre reimplements — so 100% agreement proves compatibility, not
  correctness. Issue #40 lived there at a perfect score.

This suite uses a **real browser as the oracle**. Both engines render the same
HTML to PNG and both PNGs are measured by the *same* instrument, so an
instrument error cancels instead of masquerading as a renderer bug.

## Running it

The package is behind the `chromium` build tag, so `go test ./...` — and CI's
default job — never needs a browser.

```bash
cd test
go test -tags chromium ./chromium/ -run TestChromiumParity -v
```

A missing browser **skips** (reported as environmental, not as a geometry
failure). To make it fatal — what CI does — set:

```bash
CHROMIUM_REQUIRED=1 go test -tags chromium ./chromium/ -run TestChromiumParity -v
```

Output is a per-fixture, per-colour table:

```
content-box-flex          red   x=0 y=0 w=300 h=100   x=0 y=0 w=300 h=100   ok
flex-gap                  green x=120 y=0 w=100 h=50  x=120 y=0 w=100 h=50  ok
min-max-width             red   x=0 y=0 w=150 h=50    x=0 y=0 w=50 h=50     DIFF 100px
24 fixtures, 0 failed, 7 known defects
```

`DIFF npx` is the largest per-field delta; `MISSING` means one engine did not
paint the colour at all.

### Browser resolution

In order: `$CHROMIUM_BIN`, then `chromium` / `chromium-browser` /
`google-chrome` / `google-chrome-stable` on `PATH`, then the newest Playwright
download in `~/.cache/ms-playwright` (or `~/Library/Caches` on macOS). Each
candidate is validated with `--version` before use.

```bash
npx playwright install chromium   # if nothing is installed
```

## The measuring contract

**Palette.** Fixtures paint probes with these eight exact colours and nothing
else. Every pair is ≥64 apart in at least one channel, so an antialiased edge
of one can never be read as another:

| name | hex |
|---|---|
| red | `#ff0000` |
| green | `#00ff00` |
| blue | `#0000ff` |
| yellow | `#ffff00` |
| magenta | `#ff00ff` |
| cyan | `#00ffff` |
| orange | `#ff8000` |
| black | `#000000` |

**Measurement.** Each PNG is composited over white, then every palette colour
is located and reduced to a bounding box `{x, y, w, h}`. Colours absent from
the image are absent from the result.

**Tolerance.** Two numbers, both deliberately small:

- `MatchTolerance = 8` — per-channel distance still counted as a colour match.
  Only absorbs premultiplied-alpha rounding on half-covered edge pixels; filled
  rectangles land on integer coordinates, so interiors are exact.
- `Tolerance = 1` px — per-field difference between the two boxes that still
  counts as equal. The 1px allowance covers line-height rounding; a real layout
  bug is off by tens of pixels.

**Canvas.** Each fixture declares its own `Width` / `Height` in the table, and
both engines are given exactly that viewport. A fixture that relies on the
default viewport will drift when the browser changes it.

**Fonts.** A fixture that measures text sets `Text: true`, which hands both
engines the *same bytes* of Go Regular via a data URI. Comparing Ogre to a
browser that fell back to a different font measures metrics, not layout — a
15–19% width difference with no bug behind it.

## Adding a fixture

1. Write `fixtures/<name>.html` — a **fragment**, not a document. Both engines
   get `html,body{margin:0;padding:0;background:#ffffff}` injected, so a
   `<body>` wrapper is unnecessary and `margin` is not yours to set.
2. Paint every probe with a palette colour, and give each measured element a
   `background-color`. Unpainted elements are invisible to the instrument.
3. Add an entry to `Fixtures` in `parity_test.go` with its canvas size:

```go
{
    Name: "flex-gap", File: "flex-gap.html",
    Width: 400, Height: 100,
},
```

4. Run it. Then decide, honestly, which of the two outcomes you have:
   - **It passes** — the behaviour is pinned. Done.
   - **It fails** — you have found a real defect. File it upstream, then mark
     the fixture:

```go
{
    Name: "min-max-width", File: "min-max-width.html",
    Width: 400, Height: 100,
    Known: "https://github.com/macawls/ogre/issues/45",
},
```

A known fixture's differences are reported as `known` instead of failing the
run — **but a known fixture that starts matching FAILS the run** with `FIXED?`,
so a marker cannot outlive its defect. When the fix lands, the suite tells you
to remove the marker.

**Never mark a fixture known to make a run green.** The marker is a record that
a defect exists, not a silencer; if the numbers in the fixture are not the
numbers you measured on both engines, the fixture is wrong, not the renderer.

## Current known defects

| fixture | issue | measured (Chromium vs Ogre) |
|---|---|---|
| `absolute-auto-bottom` | #40 | `top`+`bottom` unresolved on the auto path |
| `percentage-padding` | #42 | `w=240` vs `w=200` |
| `margin-auto-center` | #42 | `x=150` vs `x=0` |
| `inline-block-baseline` | #43 | baseline ignored, `y` off by 7–11px |
| `relative-offset` | #44 | `x=50 y=50` vs `x=0 y=0` |
| `absolute-in-relative-parent` | #44 | same cause: the parent box did not move |
| `min-max-width` | #45 | `w=150` vs `w=50`, siblings off by 100px |

Each of those issues carries its controls — the neighbouring case that *does*
match, which is what pins the defect to one code path.

## Operational notes

Both of these cost hours to find, and both are encoded in `browser.go`:

- A fresh `--user-data-dir` hangs headless Chromium on first-run profile setup
  (measured: 20s timeout, zero-byte PNG). The session must be `--incognito`.
- Chromium's children survive a parent crash. The leaked address space then
  makes the *next* launch fail with `pthread_create: Resource temporarily
  unavailable`. So each launch runs in its own process group and the group is
  killed afterwards whether it exited, crashed or timed out.

Two flags are known to break it and must not be added: `--single-process`
(`Trace/breakpoint trap`, rc=133) and the network-disable set
(`--host-resolver-rules`, `--disable-background-networking`, …) which hangs for
120s.

A Chromium crash leaves a `core` file in the package directory; `.gitignore`
covers it.
