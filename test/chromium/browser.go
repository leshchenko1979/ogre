//go:build chromium

package chromium

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/macawls/ogre/v3"
	"golang.org/x/image/font/gofont/goregular"
)

// ErrNoScreenshot reports that Chromium never produced an image at all.
//
// It is deliberately distinct from a geometry mismatch: a browser that fails to
// start says nothing about Ogre's layout, and conflating the two would let an
// environmental problem masquerade as a renderer defect (or, worse, hide one).
var ErrNoScreenshot = errors.New("chromium produced no screenshot")

// Browser is a resolved Chromium binary plus a scratch profile directory.
type Browser struct {
	Path    string
	Version string

	profileDir string
}

// ResolveBrowser finds a Chromium to use as the oracle.
//
// Order: $CHROMIUM_BIN, then the usual names on PATH, then the Playwright
// download cache (newest first). Every candidate is probed with --version so a
// stale wrapper script cannot be mistaken for a working browser.
func ResolveBrowser(scratchDir string) (*Browser, error) {
	var candidates []string

	if p := os.Getenv("CHROMIUM_BIN"); p != "" {
		candidates = append(candidates, p)
	}
	for _, name := range []string{
		"chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
		"chrome", "chrome-headless-shell",
	} {
		if p, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, p)
		}
	}
	candidates = append(candidates, playwrightCandidates()...)

	var tried []string
	for _, p := range candidates {
		ver, err := probeBrowser(p)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		profile, err := os.MkdirTemp(scratchDir, "chrome-profile-")
		if err != nil {
			return nil, fmt.Errorf("create profile dir: %w", err)
		}
		return &Browser{Path: p, Version: ver, profileDir: profile}, nil
	}

	if len(tried) == 0 {
		return nil, fmt.Errorf("no chromium binary found: set CHROMIUM_BIN or put chromium on PATH")
	}
	return nil, fmt.Errorf("no usable chromium binary: %s", strings.Join(tried, "; "))
}

// Close removes the scratch profile directory.
func (b *Browser) Close() {
	if b.profileDir != "" {
		_ = os.RemoveAll(b.profileDir)
	}
}

func probeBrowser(path string) (string, error) {
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// playwrightCandidates lists browser binaries from the Playwright download
// cache, newest version first.
func playwrightCandidates() []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots,
			filepath.Join(home, ".cache", "ms-playwright"),
			filepath.Join(home, "Library", "Caches", "ms-playwright"),
		)
	}
	patterns := []string{
		"chromium-*/chrome-linux*/chrome",
		"chromium-*/chrome-mac*/Chromium.app/Contents/MacOS/Chromium",
		"chromium_headless_shell-*/chrome-headless-shell-linux64/chrome-headless-shell",
		"chromium_headless_shell-*/chrome-headless-shell-mac*/chrome-headless-shell",
	}

	type hit struct {
		version int
		path    string
	}
	var hits []hit
	for _, root := range roots {
		for _, pat := range patterns {
			matches, err := filepath.Glob(filepath.Join(root, pat))
			if err != nil {
				continue
			}
			for _, m := range matches {
				if st, err := os.Stat(m); err != nil || st.IsDir() {
					continue
				}
				hits = append(hits, hit{version: playwrightVersion(m), path: m})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].version != hits[j].version {
			return hits[i].version > hits[j].version
		}
		return hits[i].path < hits[j].path
	})

	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.path)
	}
	return out
}

// playwrightVersion extracts N from a path containing ".../chromium-N/...".
func playwrightVersion(path string) int {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		i := strings.LastIndex(part, "-")
		if i < 0 {
			continue
		}
		if n, err := strconv.Atoi(part[i+1:]); err == nil {
			return n
		}
	}
	return 0
}

// Screenshot renders an HTML file at exactly w×h and writes a PNG to pngPath.
//
// The flags pin everything that would otherwise vary between machines: device
// scale factor, scrollbars, the LCD-subpixel text filter and the default
// background (Chromium's is transparent, Ogre's is white).
// launchTimeout bounds one browser launch. A headless screenshot of a small
// document takes 2-3s; anything near this bound is a hang, and a hung launch
// must fail the test rather than stall the suite.
const launchTimeout = 45 * time.Second

// Screenshot renders an HTML file at exactly w×h and writes a PNG to pngPath.
//
// The flags pin everything that would otherwise vary between machines: device
// scale factor, scrollbars and the default background (Chromium's is
// transparent, Ogre's is white).
//
// A launch is attempted twice. The box this suite runs on is memory-tight, and
// when address-space commit is exhausted Chromium's renderer children die with
// `pthread_create: Resource temporarily unavailable`, leaving a zero-byte
// screenshot; the retry clears a transient squeeze without hiding a real
// failure, since a genuine layout bug never prevents the screenshot appearing.
func (b *Browser) Screenshot(htmlPath, pngPath string, w, h int) error {
	abs, err := filepath.Abs(htmlPath)
	if err != nil {
		return err
	}
	fileURL := (&url.URL{Scheme: "file", Path: abs}).String()

	args := []string{
		"--headless",
		"--no-sandbox",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--hide-scrollbars",
		"--force-device-scale-factor=1",
		"--default-background-color=ffffffff",
		"--virtual-time-budget=3000",
		"--run-all-compositor-stages-before-draw",
		"--no-first-run",
		"--no-default-browser-check",
		// A fresh --user-data-dir hangs headless Chromium on first-run profile
		// setup (measured: 20s timeout, zero-byte screenshot) unless the
		// session is incognito, which skips the profile bootstrap entirely.
		"--incognito",
		"--user-data-dir=" + b.profileDir,
		fmt.Sprintf("--window-size=%d,%d", w, h),
		"--screenshot=" + pngPath,
		fileURL,
	}

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		_ = os.Remove(pngPath)
		if err := b.launch(args, pngPath); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// launch runs one browser invocation and waits for it to exit or time out.
//
// The browser is started in its own process group and the GROUP is killed on
// timeout: Chromium forks a zygote plus one process per renderer, and killing
// only the parent leaves those children holding the box's address-space commit,
// which then makes the NEXT launch fail. That cascade is what a bare
// exec.CommandContext produces, and it is not hypothetical — it cost several
// minutes here before the orphaned processes were found.
func (b *Browser) launch(args []string, pngPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Cancel = func() error {
		// Negative pid targets the whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	runErr := cmd.Run()

	// Reap the group whether the browser exited, crashed or timed out: Chromium
	// forks a zygote plus one process per renderer, and a SIGABRT in the parent
	// (which this box produces when address-space commit is exhausted) leaves
	// those children running and holding commit, so the next launch fails too.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)

	st, statErr := os.Stat(pngPath)
	if statErr == nil && st.Size() > 0 {
		return nil
	}

	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 1500 {
		msg = msg[len(msg)-1500:]
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: timed out after %s (err=%v): %s", ErrNoScreenshot, launchTimeout, runErr, msg)
	}
	return fmt.Errorf("%w: (err=%v): %s", ErrNoScreenshot, runErr, msg)
}

// RenderOgre renders the same document through Ogre and returns the PNG bytes.
//
// Emoji are disabled: the default provider would fetch them over the network,
// which a parity test must never depend on.
func RenderOgre(html string, w, h int, fonts []ogre.FontSource) ([]byte, error) {
	res, err := ogre.Render(html, ogre.Options{
		Width:         w,
		Height:        h,
		Format:        ogre.FormatPNG,
		EmojiProvider: "none",
		Fonts:         fonts,
	})
	if err != nil {
		return nil, fmt.Errorf("ogre render: %w", err)
	}
	return res.Data, nil
}

// GoFontName is the family name both engines are told to use for text
// fixtures. The bytes come from golang.org/x/image, so the two engines always
// receive the identical font file regardless of platform — comparing Ogre to a
// browser that silently fell back to a different face measures font metrics,
// not layout.
const GoFontName = "gofont-parity"

// GoFont returns the Ogre font source for the parity face.
func GoFont() ogre.FontSource {
	return ogre.FontSource{
		Name:   GoFontName,
		Weight: 400,
		Style:  "normal",
		Data:   goregular.TTF,
	}
}

// goFontFaceCSS returns an @font-face rule carrying the same bytes, so
// Chromium can load the face from a data URI.
func goFontFaceCSS() string {
	enc := base64.StdEncoding.EncodeToString(goregular.TTF)
	return "@font-face{font-family:'" + GoFontName + "';font-style:normal;font-weight:400;src:url(data:font/ttf;base64," + enc + ") format('truetype');}"
}

// document wraps a body fragment into the document both engines receive.
//
// The reset is injected rather than written into every fixture: it removes the
// user-agent defaults that differ between the two engines (Chromium's 8px body
// margin) so a fixture only states what it is actually testing.
func document(body string, withFont bool) string {
	var sb strings.Builder
	sb.WriteString("<html><head><style>")
	sb.WriteString("html,body{margin:0;padding:0;background:#ffffff;}")
	if withFont {
		sb.WriteString(goFontFaceCSS())
	}
	sb.WriteString("</style></head><body>")
	sb.WriteString(body)
	sb.WriteString("</body></html>")
	return sb.String()
}

// platformName is used only in error messages.
func platformName() string { return runtime.GOOS }
