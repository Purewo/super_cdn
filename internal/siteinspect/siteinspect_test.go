package siteinspect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspectDirectoryReportsFrontendRisks(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", `<script type="module" src="/assets/app.js"></script>`)
	write("assets/app.js", `import("./chunk.js"); new URL("./worker.js", import.meta.url)`)
	write("assets/app.css", `@font-face{src:url("./font.woff2")}`)
	write("assets/font.woff2", "font")

	report, err := InspectDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.HasIndex || report.FileCount != 4 {
		t.Fatalf("unexpected report basics: %+v", report)
	}
	for _, feature := range []string{"module_scripts", "root_absolute_paths", "dynamic_import", "import_meta_url", "css_relative_urls", "fonts"} {
		if !hasFeature(report, feature) {
			t.Fatalf("missing feature %q in %+v", feature, report.Features)
		}
	}
	for _, code := range []string{"module_script", "dynamic_import", "css_relative_urls", "font_cross_origin"} {
		if !hasWarning(report, code) {
			t.Fatalf("missing warning %q in %+v", code, report.Warnings)
		}
	}
}

func TestInspectFilesWarnsMissingIndex(t *testing.T) {
	report := InspectFiles([]File{{Path: "assets/app.js", Size: 10}}, func(string, int64) ([]byte, error) {
		return []byte(`console.log("ok")`), nil
	})
	if report.HasIndex {
		t.Fatal("unexpected index")
	}
	if !hasWarning(report, "missing_index") {
		t.Fatalf("missing index warning not found: %+v", report.Warnings)
	}
}

func hasFeature(report Report, feature string) bool {
	for _, got := range report.Features {
		if got == feature {
			return true
		}
	}
	return false
}

func hasWarning(report Report, code string) bool {
	for _, got := range report.Warnings {
		if got.Code == code {
			return true
		}
	}
	return false
}
