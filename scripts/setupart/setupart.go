// The installer window is drawn by Inno Setup itself. What it cannot draw —
// the glowing backdrop, the RoboRev Dashboard mark, the page icons and the
// rounded panels — is rendered here as PNG files that installer/SetupTheme.iss
// places behind those controls. The geometry and palette follow WebPier
// scripts/icongen/setupart.go (CycleArc SetupPalette.Dark, detail panel sized
// for the SIDETALK theme). The mark is this product's own purple bot tile.
// Every pixel comes from the numbers in this file.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
)

// setupArtScale is the pixel density the overlays are rendered at. Inno scales
// the window from its dialog font, so the overlays are drawn once at 3x and
// Inno shrinks them into the box the script asks for.
const setupArtScale = 3

var (
	setupSurface       = color.NRGBA{R: 0x14, G: 0x14, B: 0x16, A: 0xFF}
	setupSurfaceBottom = color.NRGBA{R: 0x0E, G: 0x0E, B: 0x10, A: 0xFF}
	setupTextSecondary = color.NRGBA{R: 0xA9, G: 0xA9, B: 0xB3, A: 0xFF}
	setupAccent        = color.NRGBA{R: 0x8D, G: 0xBB, B: 0xFF, A: 0xFF}
	setupOverlay       = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	setupTrack         = color.NRGBA{R: 0x2A, G: 0x2A, B: 0x30, A: 0xFF}
	setupPanel         = color.NRGBA{R: 0x1B, G: 0x1B, B: 0x1F, A: 0xFF}
	setupDanger        = color.NRGBA{R: 0xFF, G: 0x7B, B: 0x7B, A: 0xFF}
	setupBrandTop      = color.NRGBA{R: 0x90, G: 0x61, B: 0xF9, A: 0xFF}
	setupBrandBottom   = color.NRGBA{R: 0x4F, G: 0x2A, B: 0xCC, A: 0xFF}
)

type setupGlow struct {
	x, y, radiusX, radiusY float64
	color                  color.NRGBA
	strength               float64
}

var setupGlows = []setupGlow{
	{0.15, 1.10, 0.42, 0.62, color.NRGBA{R: 0xE7, G: 0x9B, B: 0x3A, A: 0xFF}, 0.30},
	{0.50, 1.16, 0.38, 0.60, color.NRGBA{R: 0xD9, G: 0x34, B: 0x6C, A: 0xFF}, 0.26},
	{0.86, 1.10, 0.42, 0.62, color.NRGBA{R: 0x1F, G: 0xB3, B: 0xD6, A: 0xFF}, 0.30},
}

var setupBackdropScales = []int{1, 2}

const (
	setupWidth  = 600
	setupHeight = 340
)

var bayer4 = [16]float64{0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5}

type artifact struct {
	path  string
	bytes []byte
	why   string
}

func renderSetupBackdrop(scale int) *image.NRGBA {
	width, height := setupWidth*scale, setupHeight*scale
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for py := range height {
		fy := (float64(py) + 0.5) / float64(height)
		base := lerpColor(setupSurface, setupSurfaceBottom, fy)
		for px := range width {
			fx := (float64(px) + 0.5) / float64(width)
			r, g, b := float64(base.R), float64(base.G), float64(base.B)
			for _, glow := range setupGlows {
				dx := (fx - glow.x) / glow.radiusX
				dy := (fy - glow.y) / glow.radiusY
				strength := math.Exp(-2.4*(dx*dx+dy*dy)) * glow.strength
				if strength < 0.0005 {
					continue
				}
				r = 255 - (255-r)*(1-strength*float64(glow.color.R)/255)
				g = 255 - (255-g)*(1-strength*float64(glow.color.G)/255)
				b = 255 - (255-b)*(1-strength*float64(glow.color.B)/255)
			}
			dither := (bayer4[(py&3)*4+(px&3)]+0.5)/16 - 0.5
			img.SetNRGBA(px, py, color.NRGBA{R: clampByte(r + dither), G: clampByte(g + dither), B: clampByte(b + dither), A: 0xFF})
		}
	}
	return img
}

type canvas struct {
	width, height int
	r, g, b, a    []float64
}

func newCanvas(width, height int) *canvas {
	n := width * height
	return &canvas{width: width, height: height, r: make([]float64, n), g: make([]float64, n), b: make([]float64, n), a: make([]float64, n)}
}

func (c *canvas) paint(col color.NRGBA, opacity float64, distance func(x, y float64) float64) {
	for py := range c.height {
		y := float64(py) + 0.5
		for px := range c.width {
			x := float64(px) + 0.5
			coverage := clamp01(0.5 - distance(x, y))
			if coverage <= 0 {
				continue
			}
			alpha := coverage * opacity
			i := py*c.width + px
			c.r[i] = float64(col.R)/255*alpha + c.r[i]*(1-alpha)
			c.g[i] = float64(col.G)/255*alpha + c.g[i]*(1-alpha)
			c.b[i] = float64(col.B)/255*alpha + c.b[i]*(1-alpha)
			c.a[i] = alpha + c.a[i]*(1-alpha)
		}
	}
}

func (c *canvas) image() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, c.width, c.height))
	for i := range c.a {
		a := c.a[i]
		if a <= 0 {
			continue
		}
		img.Pix[i*4+0] = clampByte(c.r[i] / a * 255)
		img.Pix[i*4+1] = clampByte(c.g[i] / a * 255)
		img.Pix[i*4+2] = clampByte(c.b[i] / a * 255)
		img.Pix[i*4+3] = clampByte(a * 255)
	}
	return img
}

func roundedBoxDistance(x0, y0, x1, y1, radius float64) func(x, y float64) float64 {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-radius, (y1-y0)/2-radius
	return func(x, y float64) float64 {
		qx := math.Abs(x-cx) - hx
		qy := math.Abs(y-cy) - hy
		outside := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))
		return outside + math.Min(math.Max(qx, qy), 0) - radius
	}
}

func ringDistance(shape func(x, y float64) float64, width float64) func(x, y float64) float64 {
	return func(x, y float64) float64 { return math.Abs(shape(x, y)) - width/2 }
}

func circleDistance(cx, cy, radius float64) func(x, y float64) float64 {
	return func(x, y float64) float64 { return math.Hypot(x-cx, y-cy) - radius }
}

func segmentDistance(ax, ay, bx, by, width float64) func(x, y float64) float64 {
	return func(x, y float64) float64 {
		px, py := x-ax, y-ay
		dx, dy := bx-ax, by-ay
		t := clamp01((px*dx + py*dy) / (dx*dx + dy*dy))
		return math.Hypot(px-dx*t, py-dy*t) - width/2
	}
}

func renderSetupPanel(width, height, radius float64, fill color.NRGBA, fillOpacity, strokeOpacity float64) *image.NRGBA {
	s := float64(setupArtScale)
	c := newCanvas(int(width*s), int(height*s))
	box := roundedBoxDistance(0, 0, width*s, height*s, radius*s)
	c.paint(fill, fillOpacity, box)
	inner := roundedBoxDistance(0.5*s, 0.5*s, (width-0.5)*s, (height-0.5)*s, (radius-0.5)*s)
	if strokeOpacity > 0 {
		c.paint(setupOverlay, strokeOpacity, ringDistance(inner, s))
	}
	return c.image()
}

func renderSetupStatusIcon(failed bool) *image.NRGBA {
	const box = 44.0
	s := float64(setupArtScale)
	c := newCanvas(int(box*s), int(box*s))
	center := box / 2 * s
	radius := (box/2 - 1) * s
	accent := setupAccent
	if failed {
		accent = setupDanger
	}
	c.paint(accent, 0.13, circleDistance(center, center, radius))
	c.paint(accent, 0.85, ringDistance(circleDistance(center, center, radius-0.75*s), 1.5*s))
	stroke := 2.4 * s
	if failed {
		c.paint(accent, 1, segmentDistance(center, center-0.42*radius, center, center+0.10*radius, stroke))
		c.paint(accent, 1, circleDistance(center, center+0.38*radius, 1.6*s))
	} else {
		c.paint(accent, 1, segmentDistance(center-0.36*radius, center+0.02*radius, center-0.10*radius, center+0.28*radius, stroke))
		c.paint(accent, 1, segmentDistance(center-0.10*radius, center+0.28*radius, center+0.38*radius, center-0.24*radius, stroke))
	}
	return c.image()
}

func renderSetupCaptionGlyph(closeGlyph bool) *image.NRGBA {
	const width, height = 36.0, 30.0
	s := float64(setupArtScale)
	c := newCanvas(int(width*s), int(height*s))
	cx, cy := width/2*s, height/2*s
	half := 5 * s
	stroke := 1.1 * s
	if closeGlyph {
		c.paint(setupTextSecondary, 1, segmentDistance(cx-half, cy-half, cx+half, cy+half, stroke))
		c.paint(setupTextSecondary, 1, segmentDistance(cx-half, cy+half, cx+half, cy-half, stroke))
	} else {
		c.paint(setupTextSecondary, 1, segmentDistance(cx-half, cy, cx+half, cy, stroke))
	}
	return c.image()
}

// renderSetupBrand draws the in-app mark: a rounded purple tile and the white
// bot glyph used beside "RoboRev" in the dashboard. size is the pixel box.
func renderSetupBrand(size int) *image.NRGBA {
	s := float64(size)
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	tile := roundedBoxDistance(0.5, 0.5, s-0.5, s-0.5, s*0.22)
	for py := range size {
		y := float64(py) + 0.5
		col := lerpColor(setupBrandTop, setupBrandBottom, y/s)
		for px := range size {
			coverage := clamp01(0.5 - tile(float64(px)+0.5, y))
			if coverage <= 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{R: col.R, G: col.G, B: col.B, A: clampByte(coverage * 255)})
		}
	}
	// Lucide's bot, in a 24-unit box inset from the tile.
	ox, oy := s*0.18, s*0.16
	unit := (s * 0.64) / 24
	pt := func(x, y float64) (float64, float64) { return ox + x*unit, oy + y*unit }
	stroke := 2 * unit
	c := newCanvas(size, size)
	white := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	seg := func(x0, y0, x1, y1 float64) {
		ax, ay := pt(x0, y0)
		bx, by := pt(x1, y1)
		c.paint(white, 1, segmentDistance(ax, ay, bx, by, stroke))
	}
	hx0, hy0 := pt(4, 8)
	hx1, hy1 := pt(20, 20)
	c.paint(white, 1, ringDistance(roundedBoxDistance(hx0, hy0, hx1, hy1, 2*unit), stroke))
	seg(12, 8, 12, 4)
	seg(12, 4, 8, 4)
	seg(2, 14, 4, 14)
	seg(20, 14, 22, 14)
	seg(9, 13, 9, 15)
	seg(15, 13, 15, 15)
	glyph := c.image()
	for i := 0; i < len(img.Pix); i += 4 {
		a := float64(glyph.Pix[i+3]) / 255
		if a <= 0 {
			continue
		}
		dstA := float64(img.Pix[i+3]) / 255
		outA := a + dstA*(1-a)
		if outA <= 0 {
			continue
		}
		blend := func(src, dst uint8) uint8 {
			return clampByte((float64(src)*a + float64(dst)*dstA*(1-a)) / outA)
		}
		img.Pix[i+0] = blend(glyph.Pix[i+0], img.Pix[i+0])
		img.Pix[i+1] = blend(glyph.Pix[i+1], img.Pix[i+1])
		img.Pix[i+2] = blend(glyph.Pix[i+2], img.Pix[i+2])
		img.Pix[i+3] = clampByte(outA * 255)
	}
	return img
}

func setupArtifacts() ([]artifact, error) {
	s := setupArtScale
	type rendered struct {
		name  string
		image *image.NRGBA
		why   string
	}
	images := []rendered{
		{"setup-brand.png", renderSetupBrand(18 * s), "window brand mark, 18px at 3x"},
		{"setup-logo.png", renderSetupBrand(44 * s), "confirmation page logo, 44px at 3x"},
		{"setup-done.png", renderSetupStatusIcon(false), "completion page icon"},
		{"setup-failed.png", renderSetupStatusIcon(true), "failure page icon"},
		{"setup-panel-location.png", renderSetupPanel(440, 38, 9, setupOverlay, 0.045, 0.09), "install location panel"},
		{"setup-panel-detail.png", renderSetupPanel(520, 140, 10, setupPanel, 1, 0.07), "failure detail panel"},
		{"setup-track.png", renderSetupPanel(440, 4, 2, setupTrack, 1, 0), "progress track"},
		{"setup-minimize.png", renderSetupCaptionGlyph(false), "minimise button glyph"},
		{"setup-close.png", renderSetupCaptionGlyph(true), "close button glyph"},
	}
	for _, scale := range setupBackdropScales {
		images = append(images, rendered{
			name:  fmt.Sprintf("setup-backdrop-%dx.png", scale),
			image: renderSetupBackdrop(scale),
			why:   fmt.Sprintf("window backdrop, %dx", scale),
		})
	}
	artifacts := make([]artifact, 0, len(images))
	for _, item := range images {
		encoded, err := encodePNG(item.image)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", item.name, err)
		}
		artifacts = append(artifacts, artifact{path: "assets/installer/" + item.name, bytes: encoded, why: item.why})
	}
	return artifacts, nil
}

func encodePNG(img image.Image) ([]byte, error) {
	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func lerpColor(from, to color.NRGBA, amount float64) color.NRGBA {
	mix := func(a, b uint8) uint8 { return clampByte(float64(a) + (float64(b)-float64(a))*amount) }
	return color.NRGBA{R: mix(from.R, to.R), G: mix(from.G, to.G), B: mix(from.B, to.B), A: 0xFF}
}

func clamp01(value float64) float64 { return math.Max(0, math.Min(1, value)) }

func clampByte(value float64) uint8 {
	return uint8(math.Max(0, math.Min(255, math.Round(value))))
}
