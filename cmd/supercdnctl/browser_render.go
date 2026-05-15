package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"supercdn/internal/siteprobe"
)

const (
	defaultBrowserRenderWidth       = 1280
	defaultBrowserRenderHeight      = 720
	defaultBrowserRenderTimeout     = 15 * time.Second
	defaultBrowserVirtualTimeBudget = 5 * time.Second
	defaultBrowserNonWhiteThreshold = 0.001
)

type browserRenderOptions struct {
	URL                string
	BrowserPath        string
	Timeout            time.Duration
	VirtualTimeBudget  time.Duration
	Width              int
	Height             int
	NonWhiteThreshold  float64
	KeepScreenshotPath string
}

func runBrowserRenderCheck(parent context.Context, opts browserRenderOptions) siteprobe.BrowserCheck {
	start := time.Now()
	opts = normalizeBrowserRenderOptions(opts)
	check := siteprobe.BrowserCheck{
		URL:       strings.TrimSpace(opts.URL),
		Width:     opts.Width,
		Height:    opts.Height,
		Threshold: opts.NonWhiteThreshold,
	}
	browser, err := findBrowserExecutable(opts.BrowserPath)
	if err != nil {
		check.Error = err.Error()
		check.Duration = time.Since(start).Milliseconds()
		return check
	}
	check.BrowserPath = browser
	screenshotPath, cleanup, err := browserScreenshotPath(opts.KeepScreenshotPath)
	if err != nil {
		check.Error = err.Error()
		check.Duration = time.Since(start).Milliseconds()
		return check
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(parent, opts.Timeout)
	defer cancel()
	args := []string{
		"--headless=new",
		"--disable-gpu",
		"--disable-dev-shm-usage",
		"--no-first-run",
		"--disable-default-apps",
		"--hide-scrollbars",
		fmt.Sprintf("--window-size=%d,%d", opts.Width, opts.Height),
		fmt.Sprintf("--virtual-time-budget=%d", opts.VirtualTimeBudget.Milliseconds()),
		"--screenshot=" + screenshotPath,
		check.URL,
	}
	if runtime.GOOS == "linux" {
		args = append([]string{"--no-sandbox"}, args...)
	}
	cmd := exec.CommandContext(ctx, browser, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		check.Error = "browser render timed out"
		check.Duration = time.Since(start).Milliseconds()
		return check
	}
	if err != nil {
		check.Error = strings.TrimSpace(firstNonEmpty(string(output), err.Error()))
		check.Duration = time.Since(start).Milliseconds()
		return check
	}
	analysis, err := analyzeBrowserScreenshot(screenshotPath, opts.NonWhiteThreshold)
	check.Width = analysis.Width
	check.Height = analysis.Height
	check.NonWhiteRatio = analysis.NonWhiteRatio
	check.NonWhitePixels = analysis.NonWhitePixels
	check.PixelCount = analysis.PixelCount
	check.OK = analysis.OK
	check.Error = analysis.Error
	check.Warnings = append(check.Warnings, analysis.Warnings...)
	if err != nil {
		check.Error = err.Error()
	}
	check.Duration = time.Since(start).Milliseconds()
	return check
}

func normalizeBrowserRenderOptions(opts browserRenderOptions) browserRenderOptions {
	opts.URL = strings.TrimSpace(opts.URL)
	opts.BrowserPath = strings.TrimSpace(opts.BrowserPath)
	if opts.Timeout <= 0 {
		opts.Timeout = defaultBrowserRenderTimeout
	}
	if opts.VirtualTimeBudget <= 0 {
		opts.VirtualTimeBudget = defaultBrowserVirtualTimeBudget
	}
	if opts.Width <= 0 {
		opts.Width = defaultBrowserRenderWidth
	}
	if opts.Height <= 0 {
		opts.Height = defaultBrowserRenderHeight
	}
	if opts.NonWhiteThreshold <= 0 {
		opts.NonWhiteThreshold = defaultBrowserNonWhiteThreshold
	}
	return opts
}

func browserScreenshotPath(keep string) (string, func(), error) {
	keep = strings.TrimSpace(keep)
	if keep != "" {
		return keep, func() {}, nil
	}
	file, err := os.CreateTemp("", "supercdn-browser-render-*.png")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", func() {}, err
	}
	return path, func() { _ = os.Remove(path) }, nil
}

func findBrowserExecutable(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		if fileExists(explicit) {
			return explicit, nil
		}
		return "", fmt.Errorf("browser executable not found: %s", explicit)
	}
	for _, env := range []string{"SUPERCDN_BROWSER", "CHROME", "EDGE"} {
		if candidate := strings.TrimSpace(os.Getenv(env)); candidate != "" && fileExists(candidate) {
			return candidate, nil
		}
	}
	for _, name := range []string{"chrome", "google-chrome", "chromium", "chromium-browser", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	for _, candidate := range browserExecutableCandidates() {
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("Chrome or Edge executable not found; pass -browser-path or set SUPERCDN_BROWSER")
}

func browserExecutableCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		}
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		return []string{
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/microsoft-edge",
		}
	}
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func analyzeBrowserScreenshot(path string, threshold float64) (siteprobe.BrowserCheck, error) {
	if threshold <= 0 {
		threshold = defaultBrowserNonWhiteThreshold
	}
	file, err := os.Open(path)
	if err != nil {
		return siteprobe.BrowserCheck{Threshold: threshold}, err
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		return siteprobe.BrowserCheck{Threshold: threshold}, err
	}
	bounds := img.Bounds()
	total := bounds.Dx() * bounds.Dy()
	check := siteprobe.BrowserCheck{
		Width:      bounds.Dx(),
		Height:     bounds.Dy(),
		PixelCount: total,
		Threshold:  threshold,
	}
	if total <= 0 {
		check.Error = "browser screenshot is empty"
		return check, errors.New(check.Error)
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if nonWhitePixel(img.At(x, y)) {
				check.NonWhitePixels++
			}
		}
	}
	check.NonWhiteRatio = float64(check.NonWhitePixels) / float64(total)
	check.OK = check.NonWhiteRatio >= threshold
	if !check.OK {
		check.Error = fmt.Sprintf("browser screenshot looks blank: non-white ratio %.6f below %.6f", check.NonWhiteRatio, threshold)
	}
	return check, nil
}

func nonWhitePixel(c color.Color) bool {
	r, g, b, a := c.RGBA()
	if a == 0 {
		return false
	}
	return r>>8 < 245 || g>>8 < 245 || b>>8 < 245
}
