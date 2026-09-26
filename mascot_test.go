package scout

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMascotSVGMatchesDocs keeps the embedded drawing and the shipped asset in
// step: whichever one is edited, the other must follow.
func TestMascotSVGMatchesDocs(t *testing.T) {
	b, err := os.ReadFile("docs/mascot.svg")
	if err != nil {
		t.Fatalf("read docs/mascot.svg: %v", err)
	}
	if got, want := squash(MascotSVG), squash(string(b)); got != want {
		t.Errorf("MascotSVG differs from docs/mascot.svg; run: go run ./cmd/scout mascot -svg > docs/mascot.svg")
	}
}

// TestMascotASCIIisASCII guards the CLI banner against double-width characters,
// which would break the column alignment of the legend.
func TestMascotASCIIisASCII(t *testing.T) {
	for i, r := range MascotASCII {
		if r > 127 {
			t.Fatalf("MascotASCII has non-ASCII %q at byte %d", r, i)
		}
	}
	if !strings.Contains(MascotASCII, "go-scout") {
		t.Error("MascotASCII should carry the project name")
	}
}

// TestDocumentationSVGsWellFormed parses every SVG shipped in docs/, including
// the 12 translated diagram sets, so a malformed hand-written asset fails the
// build instead of rendering as a broken image on GitHub.
func TestDocumentationSVGsWellFormed(t *testing.T) {
	var files int
	err := filepath.WalkDir("docs", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".svg") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc struct {
			XMLName xml.Name
		}
		if err := xml.Unmarshal(b, &doc); err != nil {
			t.Errorf("%s: malformed XML: %v", path, err)
			return nil
		}
		if doc.XMLName.Local != "svg" {
			t.Errorf("%s: root element is <%s>, want <svg>", path, doc.XMLName.Local)
		}
		files++
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	if files < 50 { // 4 diagrams x 13 languages, plus mascot and logo
		t.Errorf("only %d SVG files found, expected the whole docs/ set", files)
	}
}

// squash collapses whitespace so formatting differences between the .go literal
// and the .svg file do not count as a mismatch.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
