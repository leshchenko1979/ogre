//go:build chromium

package chromium

import (
	"os"
	"testing"
)

// resolveOrSkip returns a browser, or skips the test when none is available.
//
// A skip is the default because the suite is optional locally. CI sets
// CHROMIUM_REQUIRED=1, which turns a missing browser into a failure: a gate
// that silently skips is a gate that reports green while testing nothing.
func resolveOrSkip(t *testing.T) *Browser {
	t.Helper()

	b, err := ResolveBrowser(t.TempDir())
	if err != nil {
		if os.Getenv("CHROMIUM_REQUIRED") != "" {
			t.Fatalf("CHROMIUM_REQUIRED is set but no browser is usable: %v", err)
		}
		t.Skipf("no Chromium available (%v); set CHROMIUM_BIN or CHROMIUM_REQUIRED=1", err)
	}
	t.Cleanup(b.Close)
	t.Logf("oracle: %s (%s)", b.Path, b.Version)
	return b
}

// skipOrFail reports an environmental problem: a skip locally, a failure when
// CHROMIUM_REQUIRED is set. A gate that silently skips in CI would report green
// while testing nothing, which is why CI sets the variable.
func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CHROMIUM_REQUIRED") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}
