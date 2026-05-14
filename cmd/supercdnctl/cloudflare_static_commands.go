package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	urlpath "path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

func publishCloudflareStatic(args []string) error {
	fs := flag.NewFlagSet("publish-cloudflare-static", flag.ExitOnError)
	site := fs.String("site", "", "site id used to derive the default Worker name")
	worker := fs.String("name", "", "Cloudflare Worker name; defaults to supercdn-{site}-static")
	dir := fs.String("dir", "", "static asset directory")
	domains := fs.String("domains", "", "comma-separated custom domains")
	compatDate := fs.String("compatibility-date", time.Now().UTC().Format("2006-01-02"), "Workers compatibility date")
	envFile := fs.String("env-file", "configs/private/cloudflare.env", "local env file containing CF_API_TOKEN and CF_ACCOUNT_ID; empty to skip")
	wrangler := fs.String("wrangler", "npx", "wrangler executable; default uses npx --prefix worker wrangler")
	wranglerPrefix := fs.String("wrangler-prefix", "worker", "npm package directory when -wrangler is npx")
	message := fs.String("message", "", "deployment message")
	cachePolicy := fs.String("static-cache-policy", cloudflareStaticCachePolicyAuto, "Cloudflare Static cache policy: auto, force, or none")
	notFoundHandling := fs.String("static-not-found-handling", "", "Cloudflare Static not_found_handling: none, 404-page, or single-page-application")
	spa := fs.Bool("static-spa", false, "enable Cloudflare Static single-page-application fallback")
	dryRun := fs.Bool("dry-run", true, "plan deployment without modifying Cloudflare; pass -dry-run=false to deploy")
	_ = fs.Parse(args)
	resp, err := runCloudflareStaticPublish(cloudflareStaticPublishOptions{
		Site:              *site,
		WorkerName:        *worker,
		Dir:               *dir,
		Domains:           splitCSV(*domains),
		CompatibilityDate: *compatDate,
		EnvFile:           *envFile,
		Wrangler:          *wrangler,
		WranglerPrefix:    *wranglerPrefix,
		Message:           *message,
		CachePolicy:       *cachePolicy,
		NotFoundHandling:  cloudflareStaticNotFoundHandlingFlag(*notFoundHandling, *spa),
		DryRun:            *dryRun,
	})
	if err != nil {
		raw, _ := json.Marshal(resp)
		_ = printJSON(raw)
		return err
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func runCloudflareStaticPublish(opts cloudflareStaticPublishOptions) (cloudflareStaticPublishResponse, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return cloudflareStaticPublishResponse{}, errors.New("-dir is required")
	}
	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	if !info.IsDir() {
		return cloudflareStaticPublishResponse{}, fmt.Errorf("%s is not a directory", opts.Dir)
	}
	preparedDir, cleanup, headers, err := prepareCloudflareStaticAssetsDir(absDir, opts.CachePolicy)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	if cleanup != nil {
		defer cleanup()
	}
	workerName := strings.TrimSpace(opts.WorkerName)
	if workerName == "" {
		if strings.TrimSpace(opts.Site) == "" {
			return cloudflareStaticPublishResponse{}, errors.New("-site or -name is required")
		}
		workerName = "supercdn-" + cleanWorkerName(opts.Site) + "-static"
	}
	notFoundHandling, err := normalizeCloudflareStaticNotFoundHandling(opts.NotFoundHandling)
	if err != nil {
		return cloudflareStaticPublishResponse{}, err
	}
	wranglerConfig := ""
	var configCleanup func()
	if notFoundHandling != "" {
		wranglerConfig, configCleanup, err = writeCloudflareStaticWranglerConfig(workerName, preparedDir, opts.CompatibilityDate, notFoundHandling)
		if err != nil {
			return cloudflareStaticPublishResponse{}, err
		}
		defer configCleanup()
	}
	wrangler := firstNonEmpty(strings.TrimSpace(opts.Wrangler), "npx")
	cmdArgs := wranglerDeployArgs(wrangler, opts.WranglerPrefix, workerName, preparedDir, opts.Domains, opts.CompatibilityDate, opts.Message, opts.DryRun, wranglerConfig)
	resp := cloudflareStaticPublishResponse{
		Status:            "planned",
		DryRun:            opts.DryRun,
		Worker:            workerName,
		AssetsDir:         preparedDir,
		SourceDir:         absDir,
		Domains:           opts.Domains,
		CompatibilityDate: strings.TrimSpace(opts.CompatibilityDate),
		CachePolicy:       headers.Policy,
		NotFoundHandling:  notFoundHandling,
		WranglerConfig:    wranglerConfig,
		HeadersFile:       headers.Path,
		HeadersSource:     headers.Source,
		HeadersGenerated:  headers.Generated,
		Command:           append([]string{wrangler}, cmdArgs...),
	}
	env, err := cloudflareStaticEnv(opts.EnvFile)
	if err != nil {
		return resp, err
	}
	out, exitCode, err := runCommand(wrangler, cmdArgs, env)
	resp.Output = strings.TrimSpace(out)
	resp.ExitCode = exitCode
	if err != nil {
		resp.Status = "failed"
		return resp, err
	}
	if opts.DryRun {
		resp.Status = "planned"
	} else {
		resp.Status = "ok"
	}
	return resp, nil
}

func wranglerDeployArgs(wrangler, wranglerPrefix, workerName, assetsDir string, domains []string, compatDate, message string, dryRun bool, configPath string) []string {
	var args []string
	if filepath.Base(strings.ToLower(strings.TrimSpace(wrangler))) == "npx" && strings.TrimSpace(wranglerPrefix) != "" {
		args = append(args, "--prefix", wranglerPrefix, "wrangler")
	}
	args = append(args, "deploy")
	if strings.TrimSpace(configPath) != "" {
		args = append(args, "--config", configPath)
	} else {
		args = append(args, "--name", workerName, "--compatibility-date", strings.TrimSpace(compatDate), "--assets", assetsDir)
	}
	for _, domain := range domains {
		args = append(args, "--domain", domain)
	}
	if strings.TrimSpace(message) != "" {
		args = append(args, "--message", strings.TrimSpace(message))
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

type cloudflareStaticHeadersMeta struct {
	Policy    string
	Path      string
	Source    string
	Generated bool
}

func prepareCloudflareStaticAssetsDir(sourceDir, policy string) (string, func(), cloudflareStaticHeadersMeta, error) {
	policy, err := normalizeCloudflareStaticCachePolicy(policy)
	if err != nil {
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	existingHeaders := filepath.Join(sourceDir, "_headers")
	if policy == cloudflareStaticCachePolicyNone {
		return sourceDir, nil, cloudflareStaticHeadersMeta{Policy: policy, Path: existingHeaders, Source: "disabled"}, nil
	}
	if policy == cloudflareStaticCachePolicyAuto {
		if info, err := os.Stat(existingHeaders); err == nil && !info.IsDir() {
			return sourceDir, nil, cloudflareStaticHeadersMeta{Policy: policy, Path: existingHeaders, Source: "existing"}, nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", nil, cloudflareStaticHeadersMeta{}, err
		}
	}
	tmp, err := os.MkdirTemp("", "supercdn-cloudflare-static-*")
	if err != nil {
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if err := copyDirectory(sourceDir, tmp); err != nil {
		cleanup()
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	headersPath := filepath.Join(tmp, "_headers")
	if err := os.WriteFile(headersPath, []byte(generatedCloudflareStaticHeaders(sourceDir)), 0644); err != nil {
		cleanup()
		return "", nil, cloudflareStaticHeadersMeta{}, err
	}
	return tmp, cleanup, cloudflareStaticHeadersMeta{Policy: policy, Path: headersPath, Source: "generated", Generated: true}, nil
}

func normalizeCloudflareStaticCachePolicy(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return cloudflareStaticCachePolicyAuto, nil
	}
	switch value {
	case cloudflareStaticCachePolicyAuto, cloudflareStaticCachePolicyForce, cloudflareStaticCachePolicyNone:
		return value, nil
	default:
		return "", fmt.Errorf("static cache policy must be auto, force or none")
	}
}

func cloudflareStaticNotFoundHandlingFlag(value string, spa bool) string {
	if spa {
		return cloudflareStaticNotFoundSPA
	}
	return value
}

func normalizeCloudflareStaticNotFoundHandling(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == cloudflareStaticNotFoundNone {
		return "", nil
	}
	switch value {
	case cloudflareStaticNotFound404, cloudflareStaticNotFoundSPA:
		return value, nil
	default:
		return "", fmt.Errorf("static not found handling must be none, 404-page or single-page-application")
	}
}

func writeCloudflareStaticWranglerConfig(workerName, assetsDir, compatDate, notFoundHandling string) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "supercdn-wrangler-config-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	configPath := filepath.Join(tmp, "wrangler.toml")
	var b strings.Builder
	b.WriteString("name = " + strconv.Quote(workerName) + "\n")
	b.WriteString("compatibility_date = " + strconv.Quote(strings.TrimSpace(compatDate)) + "\n\n")
	b.WriteString("[assets]\n")
	b.WriteString("directory = " + tomlPathString(assetsDir) + "\n")
	b.WriteString("not_found_handling = " + strconv.Quote(notFoundHandling) + "\n")
	if err := os.WriteFile(configPath, []byte(b.String()), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return configPath, cleanup, nil
}

func copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cloudflare_static assets do not support symlink: %s", p)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			_ = in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		inErr := in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if inErr != nil {
			return inErr
		}
		return closeErr
	})
}

func generatedCloudflareStaticHeaders(root string) string {
	files := listCloudflareStaticHeaderFiles(root)
	versionedRefs := versionedAssetReferences(root)
	var b strings.Builder
	b.WriteString("# Generated by SuperCDN. Do not edit in-place; change the deploy command or provide your own _headers file.\n")
	b.WriteString("# HTML stays revalidating. Versioned/build assets get immutable browser caching.\n\n")
	b.WriteString("/\n")
	b.WriteString("  Cache-Control: " + cloudflareStaticHTMLCacheControl + "\n\n")
	for _, rel := range files {
		publicPath := "/" + filepath.ToSlash(rel)
		if publicPath == "/_headers" || publicPath == "/_redirects" {
			continue
		}
		b.WriteString(publicPath + "\n")
		b.WriteString("  Cache-Control: " + cloudflareStaticCacheControlForPath(publicPath, versionedRefs) + "\n\n")
	}
	return b.String()
}

func listCloudflareStaticHeaderFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	sort.Slice(files, func(i, j int) bool {
		return filepath.ToSlash(files[i]) < filepath.ToSlash(files[j])
	})
	return files
}

func cloudflareStaticCacheControlForPath(publicPath string, versionedRefs map[string]bool) string {
	ext := strings.ToLower(urlpath.Ext(publicPath))
	base := strings.ToLower(urlpath.Base(publicPath))
	switch {
	case ext == ".html" || ext == ".htm" || base == "sw.js" || base == "service-worker.js":
		return cloudflareStaticHTMLCacheControl
	case isCloudflareStaticAssetExtension(ext) && (versionedRefs[publicPath] || isKnownBuildAssetPath(publicPath) || filenameLooksVersioned(base)):
		return cloudflareStaticImmutableCacheControl
	default:
		return cloudflareStaticShortCacheControl
	}
}

func isCloudflareStaticAssetExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".js", ".mjs", ".css", ".json", ".wasm", ".map",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".mp4", ".webm", ".mp3", ".ogg", ".wav",
		".zip", ".gz", ".br", ".pdf", ".csv":
		return true
	default:
		return false
	}
}

func isKnownBuildAssetPath(publicPath string) bool {
	publicPath = strings.ToLower(publicPath)
	for _, prefix := range []string{"/assets/", "/static/", "/build/", "/_next/static/"} {
		if strings.HasPrefix(publicPath, prefix) {
			return true
		}
	}
	return false
}

func filenameLooksVersioned(base string) bool {
	name := strings.TrimSuffix(base, urlpath.Ext(base))
	for _, part := range filenameVersionSeparatorsRE.Split(name, -1) {
		if len(part) >= 8 && filenameVersionTokenRE.MatchString(strings.ToLower(part)) {
			hasLetter, hasDigit := false, false
			for _, r := range part {
				if r >= '0' && r <= '9' {
					hasDigit = true
				}
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
					hasLetter = true
				}
			}
			if hasLetter && hasDigit {
				return true
			}
		}
	}
	return false
}

var (
	assetRefWithQueryRE         = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']([^"']+\?[^"']*)["']`)
	filenameVersionSeparatorsRE = regexp.MustCompile(`[._-]+`)
	filenameVersionTokenRE      = regexp.MustCompile(`^[a-z0-9]+$`)
)

func versionedAssetReferences(root string) map[string]bool {
	refs := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".html") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		htmlDir := "/" + strings.Trim(strings.TrimSuffix(filepath.ToSlash(rel), urlpath.Base(filepath.ToSlash(rel))), "/")
		if htmlDir == "/" {
			htmlDir = ""
		}
		for _, match := range assetRefWithQueryRE.FindAllStringSubmatch(string(raw), -1) {
			ref := strings.TrimSpace(match[1])
			u, err := url.Parse(ref)
			if err != nil || u.IsAbs() || u.Path == "" || strings.HasPrefix(u.Path, "//") {
				continue
			}
			if !isCloudflareStaticAssetExtension(strings.ToLower(urlpath.Ext(u.Path))) {
				continue
			}
			var publicPath string
			if strings.HasPrefix(u.Path, "/") {
				publicPath = urlpath.Clean(u.Path)
			} else {
				publicPath = urlpath.Clean(urlpath.Join("/", htmlDir, u.Path))
			}
			if !strings.HasPrefix(publicPath, "/") {
				publicPath = "/" + publicPath
			}
			refs[publicPath] = true
		}
		return nil
	})
	return refs
}

func cloudflareStaticEnv(path string) ([]string, error) {
	env := os.Environ()
	values, err := readSimpleEnvFile(path)
	if err != nil {
		return nil, err
	}
	if token := firstNonEmpty(os.Getenv("CLOUDFLARE_API_TOKEN"), values["CLOUDFLARE_API_TOKEN"], values["CF_API_TOKEN"]); token != "" {
		env = append(env, "CLOUDFLARE_API_TOKEN="+token)
	}
	if accountID := firstNonEmpty(os.Getenv("CLOUDFLARE_ACCOUNT_ID"), values["CLOUDFLARE_ACCOUNT_ID"], values["CF_ACCOUNT_ID"]); accountID != "" {
		env = append(env, "CLOUDFLARE_ACCOUNT_ID="+accountID)
	}
	for key, value := range values {
		if strings.HasPrefix(key, "CF_") || strings.HasPrefix(key, "CLOUDFLARE_") {
			env = append(env, key+"="+value)
		}
	}
	return env, nil
}

func readSimpleEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	path = strings.TrimSpace(path)
	if path == "" {
		return values, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return values, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			values[key] = value
		}
	}
	return values, nil
}
