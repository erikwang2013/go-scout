package scout

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docLink matches the relative targets a README points at: markdown links and
// images `](target)`, plus HTML `src="target"`.
var docLink = regexp.MustCompile(`(?:\]\(|src=")([^)\s"]+)`)

// readmes returns README.md plus the 12 translated copies.
func readmes(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("docs/i18n/*/README.md") // constant pattern, cannot fail
	files = append([]string{"README.md"}, files...)
	if len(files) != 13 { // the root README plus 12 languages
		t.Fatalf("found %d READMEs, want 13", len(files))
	}
	return files
}

// stripFences blanks fenced code blocks, where a Go generic call such as
// `GetAs[Post](ctx, ...)` would otherwise read as a markdown link.
func stripFences(body string) string {
	var out strings.Builder
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced {
			out.WriteString(line)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// isRelative reports whether a target is a path in this repository rather than
// an absolute URL, a bare anchor, or a non-path scheme such as mailto:.
func isRelative(target string) bool {
	return !strings.HasPrefix(target, "#") && !strings.Contains(target, ":")
}

// TestReadmeLinksResolve keeps every relative target in the READMEs honest, so
// a translation written as if it sat at the repo root cannot ship 404s.
func TestReadmeLinksResolve(t *testing.T) {
	for _, file := range readmes(t) {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range docLink.FindAllStringSubmatch(stripFences(string(b)), -1) {
			target := m[1]
			if !isRelative(target) {
				continue
			}
			resolved := filepath.Join(filepath.Dir(file), filepath.FromSlash(target))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s: link %q resolves to %s, which does not exist", file, target, resolved)
			}
		}
	}
}

// TestReadmeVersionsMatch keeps a version bump from missing a translation.
func TestReadmeVersionsMatch(t *testing.T) {
	want := "v" + Version
	for _, file := range readmes(t) {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s: %s not found; bump it alongside scout.Version", file, want)
		}
	}
}
