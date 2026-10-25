package main

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSetupArtMatchesTheThemeContract(t *testing.T) {
	want := map[string][2]int{
		"setup-brand.png":          {54, 54},
		"setup-logo.png":           {132, 132},
		"setup-done.png":           {132, 132},
		"setup-failed.png":         {132, 132},
		"setup-panel-location.png": {1320, 114},
		"setup-panel-detail.png":   {1560, 420},
		"setup-track.png":          {1320, 12},
		"setup-minimize.png":       {108, 90},
		"setup-close.png":          {108, 90},
		"setup-backdrop-1x.png":    {600, 340},
		"setup-backdrop-2x.png":    {1200, 680},
	}
	seen := map[string]bool{}
	for _, item := range mustArtifacts(t) {
		if !strings.HasPrefix(item.path, "assets/installer/") {
			continue
		}
		name := strings.TrimPrefix(item.path, "assets/installer/")
		size, ok := want[name]
		if !ok {
			t.Fatalf("%s is not part of the theme contract", item.path)
		}
		seen[name] = true
		bounds := decodeSetupArt(t, item.bytes).Bounds()
		if bounds.Dx() != size[0] || bounds.Dy() != size[1] {
			t.Fatalf("%s is %dx%d, want %dx%d", name, bounds.Dx(), bounds.Dy(), size[0], size[1])
		}
		committed := readCommitted(t, name)
		if !bytes.Equal(committed, item.bytes) {
			t.Fatalf("%s differs from the generator; run go run ./scripts/setupart", name)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("%s is not generated", name)
		}
	}
}

func TestSetupOverlaysAreTransparentOutsideTheirShapes(t *testing.T) {
	for _, name := range []string{"setup-panel-location.png", "setup-panel-detail.png", "setup-done.png", "setup-failed.png", "setup-minimize.png", "setup-brand.png", "setup-logo.png"} {
		img := setupArtNamed(t, name)
		if corner := img.NRGBAAt(0, 0); corner.A != 0 {
			t.Fatalf("%s top-left corner alpha is %d, want transparent", name, corner.A)
		}
	}
	location := setupArtNamed(t, "setup-panel-location.png")
	middle := location.NRGBAAt(location.Bounds().Dx()/2, location.Bounds().Dy()/2)
	if middle.A == 0 || middle.A > 24 {
		t.Fatalf("location panel fill alpha is %d; it should be a faint veil over the backdrop", middle.A)
	}
	detail := setupArtNamed(t, "setup-panel-detail.png")
	inside := detail.NRGBAAt(detail.Bounds().Dx()/2, detail.Bounds().Dy()/2)
	if inside != setupPanel {
		t.Fatalf("detail panel fill is %v, want the opaque %v the read-only text box uses", inside, setupPanel)
	}
}

func TestSetupBackdropKeepsTheGlowLow(t *testing.T) {
	img := renderSetupBackdrop(1)
	top := img.NRGBAAt(setupWidth/2, 4)
	if absDiff(top.R, setupSurface.R) > 2 || absDiff(top.G, setupSurface.G) > 2 || absDiff(top.B, setupSurface.B) > 2 {
		t.Fatalf("top of the backdrop is %v, want the surface %v", top, setupSurface)
	}
	left := img.NRGBAAt(setupWidth/8, setupHeight-4)
	if left.R <= left.B {
		t.Fatalf("bottom-left is %v; the amber pool should make it warm", left)
	}
	right := img.NRGBAAt(setupWidth*7/8, setupHeight-4)
	if right.B <= right.R {
		t.Fatalf("bottom-right is %v; the teal pool should make it cool", right)
	}
}

func TestSetupBrandIsTheDashboardMark(t *testing.T) {
	img := setupArtNamed(t, "setup-logo.png")
	center := img.NRGBAAt(img.Bounds().Dx()/2, img.Bounds().Dy()/2)
	if center.B < center.R || center.R < 80 {
		t.Fatalf("logo center is %v; the tile should be the dashboard purple", center)
	}
	if !hasNearWhite(img) {
		t.Fatal("logo has no white bot glyph")
	}
}

func hasNearWhite(img *image.NRGBA) bool {
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] > 200 && img.Pix[i] > 240 && img.Pix[i+1] > 240 && img.Pix[i+2] > 240 {
			return true
		}
	}
	return false
}

func setupArtNamed(t *testing.T, name string) *image.NRGBA {
	t.Helper()
	for _, item := range mustArtifacts(t) {
		if item.path == "assets/installer/"+name {
			return decodeSetupArt(t, item.bytes)
		}
	}
	t.Fatalf("%s is not generated", name)
	return nil
}

func mustArtifacts(t *testing.T) []artifact {
	t.Helper()
	items, err := setupArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func decodeSetupArt(t *testing.T, raw []byte) *image.NRGBA {
	t.Helper()
	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(decoded.Bounds())
	draw.Draw(img, img.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	return img
}

func readCommitted(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the generator source")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "assets", "installer", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return raw
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
