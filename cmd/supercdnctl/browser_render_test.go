package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeBrowserScreenshotDetectsBlankAndRenderedPages(t *testing.T) {
	dir := t.TempDir()
	blank := filepath.Join(dir, "blank.png")
	rendered := filepath.Join(dir, "rendered.png")
	writeTestPNG(t, blank, color.RGBA{R: 255, G: 255, B: 255, A: 255}, nil)
	writeTestPNG(t, rendered, color.RGBA{R: 255, G: 255, B: 255, A: 255}, map[image.Point]color.Color{
		{X: 1, Y: 1}: color.RGBA{R: 10, G: 10, B: 10, A: 255},
		{X: 2, Y: 1}: color.RGBA{R: 10, G: 10, B: 10, A: 255},
		{X: 3, Y: 1}: color.RGBA{R: 10, G: 10, B: 10, A: 255},
	})

	blankCheck, err := analyzeBrowserScreenshot(blank, 0.01)
	if err != nil {
		t.Fatalf("blank screenshot returned decode error: %v", err)
	}
	if blankCheck.OK || blankCheck.Error == "" {
		t.Fatalf("blank screenshot should fail: %+v", blankCheck)
	}
	renderedCheck, err := analyzeBrowserScreenshot(rendered, 0.01)
	if err != nil {
		t.Fatalf("rendered screenshot returned error: %v", err)
	}
	if !renderedCheck.OK || renderedCheck.NonWhitePixels != 3 {
		t.Fatalf("rendered screenshot should pass: %+v", renderedCheck)
	}
}

func TestFindBrowserExecutableUsesExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "browser")
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := findBrowserExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("browser path = %q, want %q", got, path)
	}
}

func writeTestPNG(t *testing.T, path string, bg color.Color, points map[image.Point]color.Color) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.Set(x, y, bg)
		}
	}
	for point, c := range points {
		img.Set(point.X, point.Y, c)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}
