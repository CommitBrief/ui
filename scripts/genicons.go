// SPDX-License-Identifier: GPL-3.0-or-later

//go:build ignore

// genicons renders every raster icon of the product from the one vector source,
// build/appicon.svg (the CommitBrief mark). It is a standalone program, not
// part of the module's packages:
//
//	go run scripts/genicons.go        (or: make icons)
//
// It writes, relative to the repository root:
//
//	build/appicon.png                         1024 px, macOS icon grid (824 px body)
//	build/darwin/icons.icns                   macOS bundle icon, 16..1024 px
//	build/darwin/dmg-file-icon.{png,icns}     the .dmg file icon (same artwork)
//	build/darwin/dmg-background.png           540x380 disk image background
//	build/appicon.icon/Assets/mark.svg        Icon Composer foreground layer
//	build/windows/icon.ico                    16, 24, 32, 48, 64, 256 px
//	build/linux/icons/commitbrief-ui-N.png    32..512 px
//	build/linux/icons/commitbrief-ui.svg      the source, for hicolor/scalable
//	internal/adapter/desktop/trayicons/*.png  tray glyphs
//
// The geometry and colours come from the SVG; nothing is redrawn by hand.
// Rasterisation is exact (distance fields, 4x4 or 8x8 supersampling), so every
// size is drawn directly rather than scaled down from a larger one. The
// program needs only the standard library: no ImageMagick, no rsvg.
//
// build/darwin/Assets.car (the macOS 26 icon bundle) is compiled by actool and
// is therefore produced by scripts/gen-assets-car.sh, not here.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const sourceSVG = "build/appicon.svg"

type point struct{ x, y float64 }

// mark is the parsed CommitBrief mark: a rounded square with a chevron.
type mark struct {
	viewBox float64 // side of the square viewBox
	radius  float64 // corner radius of the square, viewBox units
	stroke  float64 // chevron stroke width, viewBox units
	bg, fg  color.NRGBA
	chevron []point // polyline, round caps and joins
}

// render describes one raster to draw.
type render struct {
	size  int
	body  bool         // draw the rounded square
	inset float64      // body mode: margin on every side, pixels
	pad   float64      // glyph-only mode: margin as a fraction of size
	fg    *color.NRGBA // overrides the chevron colour
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "genicons:", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat(sourceSVG); err != nil {
		return fmt.Errorf("run from the repository root: %w", err)
	}
	m, err := parseMark(sourceSVG)
	if err != nil {
		return err
	}

	// macOS: the body occupies 824 of 1024 px (the Apple icon grid), the rest is
	// transparent margin. The same proportion is used at every size.
	macAt := func(size int) render {
		return render{size: size, body: true, inset: float64(size) * 100 / 1024}
	}
	flatAt := func(size int) render { return render{size: size, body: true} }

	// Full-size PNGs.
	mac1024 := m.draw(macAt(1024))
	if err := writePNG("build/appicon.png", mac1024); err != nil {
		return err
	}
	if err := writePNG("build/darwin/dmg-file-icon.png", mac1024); err != nil {
		return err
	}

	// icns: every pixel size is drawn directly.
	macImages := map[int]*image.NRGBA{1024: mac1024}
	for _, s := range []int{16, 32, 64, 128, 256, 512} {
		macImages[s] = m.draw(macAt(s))
	}
	icns, err := encodeICNS(macImages)
	if err != nil {
		return err
	}
	if err := writeFile("build/darwin/icons.icns", icns); err != nil {
		return err
	}
	if err := writeFile("build/darwin/dmg-file-icon.icns", icns); err != nil {
		return err
	}

	// Windows: full-bleed square, one PNG entry per size.
	var icoImages []*image.NRGBA
	for _, s := range []int{16, 24, 32, 48, 64, 256} {
		icoImages = append(icoImages, m.draw(flatAt(s)))
	}
	ico, err := encodeICO(icoImages)
	if err != nil {
		return err
	}
	if err := writeFile("build/windows/icon.ico", ico); err != nil {
		return err
	}

	// Linux: full-bleed square (freedesktop icons carry their own shape).
	for _, s := range []int{32, 48, 64, 128, 256, 512} {
		name := fmt.Sprintf("build/linux/icons/commitbrief-ui-%d.png", s)
		if err := writePNG(name, m.draw(flatAt(s))); err != nil {
			return err
		}
	}
	svg, err := os.ReadFile(sourceSVG)
	if err != nil {
		return err
	}
	if err := writeFile("build/linux/icons/commitbrief-ui.svg", svg); err != nil {
		return err
	}

	// Tray. macOS gets the chevron alone as a template image (black + alpha, the
	// system recolours it), 22 pt at @2x. Windows gets one chevron glyph per
	// theme: ink on a light tray, brand green on a dark one. Linux gets the
	// full mark (green square, ink chevron) as one image: Wails on Linux has no
	// separate dark-mode icon, and the green square reads on light and dark
	// panels alike.
	black := color.NRGBA{A: 255}
	ink, green := m.fg, m.bg // the source has a green square with an ink chevron
	tray := map[string]render{
		"internal/adapter/desktop/trayicons/mac-template.png": {size: 44, pad: 0.12, fg: &black},
		"internal/adapter/desktop/trayicons/light.png":        {size: 64, pad: 0.10, fg: &ink},
		"internal/adapter/desktop/trayicons/dark.png":         {size: 64, pad: 0.10, fg: &green},
		"internal/adapter/desktop/trayicons/linux.png":        {size: 64, body: true, inset: 2},
	}
	for name, r := range tray {
		if err := writePNG(name, m.draw(r)); err != nil {
			return err
		}
	}

	// Icon Composer foreground layer: the chevron on a transparent 64 viewBox.
	// The green fill is the layer group's background fill in icon.json.
	if err := writeFile("build/appicon.icon/Assets/mark.svg", []byte(m.chevronSVG())); err != nil {
		return err
	}

	return writePNG("build/darwin/dmg-background.png", dmgBackground(m))
}

// --- source parsing ---------------------------------------------------------

type svgDoc struct {
	ViewBox string `xml:"viewBox,attr"`
	Rect    struct {
		RX   string `xml:"rx,attr"`
		Fill string `xml:"fill,attr"`
	} `xml:"rect"`
	Path struct {
		D           string `xml:"d,attr"`
		Stroke      string `xml:"stroke,attr"`
		StrokeWidth string `xml:"stroke-width,attr"`
	} `xml:"path"`
}

func parseMark(path string) (*mark, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc svgDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	vb := strings.Fields(doc.ViewBox)
	if len(vb) != 4 || vb[0] != "0" || vb[1] != "0" || vb[2] != vb[3] {
		return nil, fmt.Errorf("%s: viewBox must be a square starting at 0 0, got %q", path, doc.ViewBox)
	}
	m := &mark{}
	if m.viewBox, err = strconv.ParseFloat(vb[2], 64); err != nil {
		return nil, err
	}
	if m.radius, err = strconv.ParseFloat(doc.Rect.RX, 64); err != nil {
		return nil, fmt.Errorf("%s: rect rx: %w", path, err)
	}
	if m.stroke, err = strconv.ParseFloat(doc.Path.StrokeWidth, 64); err != nil {
		return nil, fmt.Errorf("%s: path stroke-width: %w", path, err)
	}
	if m.bg, err = parseHex(doc.Rect.Fill); err != nil {
		return nil, fmt.Errorf("%s: rect fill: %w", path, err)
	}
	if m.fg, err = parseHex(doc.Path.Stroke); err != nil {
		return nil, fmt.Errorf("%s: path stroke: %w", path, err)
	}
	if m.chevron, err = parseMoveLine(doc.Path.D); err != nil {
		return nil, fmt.Errorf("%s: path d: %w", path, err)
	}
	return m, nil
}

func parseHex(s string) (color.NRGBA, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.NRGBA{}, fmt.Errorf("want #rrggbb, got %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.NRGBA{}, err
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

// parseMoveLine reads the only path dialect the mark uses: absolute M and L.
func parseMoveLine(d string) ([]point, error) {
	fields := strings.Fields(strings.NewReplacer(",", " ", "M", " M ", "L", " L ").Replace(d))
	var pts []point
	for i := 0; i < len(fields); {
		cmd := fields[i]
		if cmd != "M" && cmd != "L" {
			return nil, fmt.Errorf("unsupported command %q (only absolute M and L)", cmd)
		}
		if i+2 >= len(fields) {
			return nil, fmt.Errorf("truncated path")
		}
		x, errX := strconv.ParseFloat(fields[i+1], 64)
		y, errY := strconv.ParseFloat(fields[i+2], 64)
		if errX != nil || errY != nil {
			return nil, fmt.Errorf("bad coordinate near %q", cmd)
		}
		pts = append(pts, point{x, y})
		i += 3
	}
	if len(pts) < 2 {
		return nil, fmt.Errorf("a chevron needs at least two points")
	}
	return pts, nil
}

// chevronSVG is the foreground layer for Icon Composer: the chevron alone,
// same viewBox and coordinates as the source, drawn on the 1024 pt canvas.
func (m *mark) chevronSVG() string {
	var b strings.Builder
	for i, p := range m.chevron {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&b, "%s%s %s ", cmd, num(p.x), num(p.y))
	}
	vb := num(m.viewBox)
	// Icon Composer sizes an SVG layer by its width and height; without them
	// the 64-unit viewBox lands as a 64 pt speck on the 1024 pt canvas.
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 %s %s">
  <path d="%s" stroke="#%02x%02x%02x" stroke-width="%s" stroke-linecap="round" stroke-linejoin="round" fill="none"/>
</svg>
`, vb, vb, strings.TrimSpace(b.String()), m.fg.R, m.fg.G, m.fg.B, num(m.stroke))
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// --- rasteriser -------------------------------------------------------------

// draw rasterises the mark. Each pixel is the average of n*n point samples; a
// sample is chevron, body or transparent, which makes the result exact up to
// the sampling grid and keeps colours straight (not premultiplied) at edges.
func (m *mark) draw(r render) *image.NRGBA {
	var scale, ox, oy float64
	if r.body {
		scale = (float64(r.size) - 2*r.inset) / m.viewBox
		ox, oy = r.inset, r.inset
	} else {
		// Glyph only: fit the chevron's bounding box (stroke included) into the
		// canvas minus the padding, centred.
		minX, minY := math.Inf(1), math.Inf(1)
		maxX, maxY := math.Inf(-1), math.Inf(-1)
		for _, p := range m.chevron {
			minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
			minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
		}
		half := m.stroke / 2
		minX, maxX, minY, maxY = minX-half, maxX+half, minY-half, maxY+half
		avail := float64(r.size) * (1 - 2*r.pad)
		scale = math.Min(avail/(maxX-minX), avail/(maxY-minY))
		ox = float64(r.size)/2 - (minX+maxX)/2*scale
		oy = float64(r.size)/2 - (minY+maxY)/2*scale
	}

	fg := m.fg
	if r.fg != nil {
		fg = *r.fg
	}
	n := 4
	if r.size < 128 {
		n = 8
	}
	half := m.stroke / 2
	mid := m.viewBox / 2
	flat := mid - m.radius

	inBody := func(u, v float64) bool {
		dx := math.Max(math.Abs(u-mid)-flat, 0)
		dy := math.Max(math.Abs(v-mid)-flat, 0)
		return u >= 0 && v >= 0 && u <= m.viewBox && v <= m.viewBox && dx*dx+dy*dy <= m.radius*m.radius
	}
	inChevron := func(u, v float64) bool {
		for i := 0; i+1 < len(m.chevron); i++ {
			if segDist(point{u, v}, m.chevron[i], m.chevron[i+1]) <= half {
				return true
			}
		}
		return false
	}

	img := image.NewNRGBA(image.Rect(0, 0, r.size, r.size))
	for py := 0; py < r.size; py++ {
		for px := 0; px < r.size; px++ {
			var sr, sg, sb, cover int
			for j := 0; j < n; j++ {
				for i := 0; i < n; i++ {
					x := float64(px) + (float64(i)+0.5)/float64(n)
					y := float64(py) + (float64(j)+0.5)/float64(n)
					u, v := (x-ox)/scale, (y-oy)/scale
					var c color.NRGBA
					switch {
					case inChevron(u, v) && (r.body && inBody(u, v) || !r.body):
						c = fg
					case r.body && inBody(u, v):
						c = m.bg
					default:
						continue
					}
					sr, sg, sb = sr+int(c.R), sg+int(c.G), sb+int(c.B)
					cover++
				}
			}
			if cover == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8((sr + cover/2) / cover),
				G: uint8((sg + cover/2) / cover),
				B: uint8((sb + cover/2) / cover),
				A: uint8((cover*255 + n*n/2) / (n * n)),
			})
		}
	}
	return img
}

func segDist(p, a, b point) float64 {
	abx, aby := b.x-a.x, b.y-a.y
	t := ((p.x-a.x)*abx + (p.y-a.y)*aby) / (abx*abx + aby*aby)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p.x-(a.x+t*abx), p.y-(a.y+t*aby))
}

// --- containers -------------------------------------------------------------

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeICNS writes an icns container of PNG payloads. The type codes are the
// documented modern ones; a size that appears as both 1x and 2x of another
// (32 px = 16@2x = 32@1x) is stored under both codes.
func encodeICNS(images map[int]*image.NRGBA) ([]byte, error) {
	entries := []struct {
		code string
		size int
	}{
		{"icp4", 16}, {"icp5", 32},
		{"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
		{"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
	}
	var body bytes.Buffer
	for _, e := range entries {
		img, ok := images[e.size]
		if !ok {
			return nil, fmt.Errorf("icns: no %d px image", e.size)
		}
		data, err := encodePNG(img)
		if err != nil {
			return nil, err
		}
		body.WriteString(e.code)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	_ = binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// encodeICO writes an ico container with one PNG-compressed entry per image
// (what Windows Vista and later, and winres, accept for every size).
func encodeICO(images []*image.NRGBA) ([]byte, error) {
	payloads := make([][]byte, len(images))
	for i, img := range images {
		data, err := encodePNG(img)
		if err != nil {
			return nil, err
		}
		payloads[i] = data
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&out, le, [3]uint16{0, 1, uint16(len(images))})
	offset := 6 + 16*len(images)
	for i, img := range images {
		side := img.Bounds().Dx()
		if side > 256 {
			return nil, fmt.Errorf("ico: %d px is larger than 256", side)
		}
		dim := byte(side)
		if side == 256 {
			dim = 0
		}
		out.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&out, le, uint16(1))  // colour planes
		_ = binary.Write(&out, le, uint16(32)) // bits per pixel
		_ = binary.Write(&out, le, uint32(len(payloads[i])))
		_ = binary.Write(&out, le, uint32(offset))
		offset += len(payloads[i])
	}
	for _, p := range payloads {
		out.Write(p)
	}
	return out.Bytes(), nil
}

// dmgBackground is the disk image window background: the diagonal gradient the
// Wails template ships, with the CommitBrief mark in its lower right corner
// where the Wails logo used to be.
func dmgBackground(m *mark) *image.NRGBA {
	const w, h = 540, 380
	stops := []struct {
		u       float64
		r, g, b float64
	}{
		{0, 10, 19, 39}, {0.1, 13, 22, 42}, {0.25, 29, 37, 56}, {0.4, 54, 62, 79},
		{0.5, 75, 82, 98}, {0.6, 98, 105, 119}, {0.75, 135, 140, 152},
		{0.9, 171, 175, 185}, {1.0, 193, 197, 205}, {1.2, 229, 232, 238},
		{1.5, 248, 250, 255},
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			u := float64(x)/(w-1) + float64(y)/(h-1)
			c := color.NRGBA{R: 248, G: 250, B: 255, A: 255}
			for i := 1; i < len(stops); i++ {
				if u <= stops[i].u {
					a, b := stops[i-1], stops[i]
					t := (u - a.u) / (b.u - a.u)
					c = color.NRGBA{
						R: uint8(math.Round(a.r + (b.r-a.r)*t)),
						G: uint8(math.Round(a.g + (b.g-a.g)*t)),
						B: uint8(math.Round(a.b + (b.b-a.b)*t)),
						A: 255,
					}
					break
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// Mark: 72 px, centred about (480, 318).
	logo := m.draw(render{size: 72, body: true})
	ox, oy := 480-36, 318-36
	for y := 0; y < 72; y++ {
		for x := 0; x < 72; x++ {
			s := logo.NRGBAAt(x, y)
			if s.A == 0 {
				continue
			}
			d := img.NRGBAAt(ox+x, oy+y)
			a := float64(s.A) / 255
			img.SetNRGBA(ox+x, oy+y, color.NRGBA{
				R: uint8(math.Round(float64(s.R)*a + float64(d.R)*(1-a))),
				G: uint8(math.Round(float64(s.G)*a + float64(d.G)*(1-a))),
				B: uint8(math.Round(float64(s.B)*a + float64(d.B)*(1-a))),
				A: 255,
			})
		}
	}
	return img
}

// --- output -----------------------------------------------------------------

func writePNG(path string, img image.Image) error {
	data, err := encodePNG(img)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return writeFile(path, data)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n", path, len(data))
	return nil
}
