// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func decodeTrayIcon(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode tray icon: %v", err)
	}
	return img
}

// The macOS template must be black plus alpha: the system recolours it, and any
// colour in it would show up as a wrong tint in one of the two menu bar themes.
func TestTrayMacTemplateIsBlackAndAlphaOnly(t *testing.T) {
	img := decodeTrayIcon(t, trayMacTemplate)
	b := img.Bounds()
	if b.Dx() != 44 || b.Dy() != 44 {
		t.Fatalf("template size = %dx%d, want 44x44 (22 pt at @2x)", b.Dx(), b.Dy())
	}
	visible := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			nrgba := image.NewNRGBA(image.Rect(0, 0, 1, 1))
			nrgba.Set(0, 0, img.At(x, y))
			c := nrgba.NRGBAAt(0, 0)
			if c.A == 0 {
				continue
			}
			visible++
			if c.R != 0 || c.G != 0 || c.B != 0 {
				t.Fatalf("pixel (%d,%d) = %v: a template icon may only be black", x, y, c)
			}
		}
	}
	if visible == 0 {
		t.Fatal("template icon is fully transparent")
	}
}

func TestTrayThemeVariantsDiffer(t *testing.T) {
	for name, data := range map[string][]byte{"light": trayLight, "dark": trayDark} {
		img := decodeTrayIcon(t, data)
		if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
			t.Fatalf("%s variant size = %dx%d, want 64x64", name, b.Dx(), b.Dy())
		}
	}
	if bytes.Equal(trayLight, trayDark) {
		t.Fatal("light and dark tray icons are identical")
	}
}

// Linux uses one full-mark icon (SetDarkModeIcon would overwrite it there), so
// it must differ from both chevron-only variants and keep its opaque green
// square visible on any panel.
func TestTrayLinuxIsSingleFullMark(t *testing.T) {
	img := decodeTrayIcon(t, trayLinux)
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("linux icon size = %dx%d, want 64x64", b.Dx(), b.Dy())
	}
	if bytes.Equal(trayLinux, trayLight) || bytes.Equal(trayLinux, trayDark) {
		t.Fatal("linux icon must be the full mark, not a chevron-only variant")
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	nrgba.Set(0, 0, img.At(8, 32)) // inside the square, left of the chevron
	if c := nrgba.NRGBAAt(0, 0); c.A != 255 || c.G < 150 {
		t.Fatalf("pixel (8,32) = %v: expected the opaque green square", c)
	}
}
