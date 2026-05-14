package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	urlpath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

func createBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-bucket", "china_all", "")
}

func createCDNBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-cdn-bucket", "overseas_r2", "public, max-age=31536000, immutable")
}

func createMobileCDNBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-mobile-cdn-bucket", "china_mobile", "public, max-age=86400")
}

func createIPFSBucket(c client, args []string) error {
	return createBucketWithDefaults(c, args, "create-ipfs-bucket", "ipfs_archive", "public, max-age=31536000, immutable")
}

func createDomesticCDNBucket(c client, args []string) error {
	fs := flag.NewFlagSet("create-domestic-cdn-bucket", flag.ExitOnError)
	slug := fs.String("slug", "", "bucket slug")
	name := fs.String("name", "", "bucket display name")
	description := fs.String("description", "", "bucket description")
	line := fs.String("line", "mobile", "domestic line: mobile, telecom, unicom, or all")
	profile := fs.String("profile", "", "explicit route profile; overrides -line")
	routingPolicy := fs.String("routing-policy", "", "routing policy name; requires matching multi-source route profile")
	types := fs.String("types", "", "comma-separated asset types: image,video,document,archive,other")
	maxCapacity := fs.Int64("max-capacity", 0, "bucket capacity limit in bytes; 0 means unlimited")
	maxFileSize := fs.Int64("max-file-size", 0, "single file limit in bytes; 0 means unlimited")
	cacheControl := fs.String("cache-control", "public, max-age=86400", "default Cache-Control value")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("-slug is required")
	}
	routeProfile := strings.TrimSpace(*profile)
	if routeProfile == "" {
		var err error
		routeProfile, err = domesticLineProfile(*line)
		if err != nil {
			return err
		}
	}
	req := map[string]any{
		"slug":                  *slug,
		"name":                  *name,
		"description":           *description,
		"route_profile":         routeProfile,
		"routing_policy":        strings.TrimSpace(*routingPolicy),
		"allowed_types":         splitCSV(*types),
		"max_capacity_bytes":    *maxCapacity,
		"max_file_size_bytes":   *maxFileSize,
		"default_cache_control": *cacheControl,
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets", req)
}

func domesticLineProfile(line string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "mobile", "cmcc", "china_mobile":
		return "china_mobile", nil
	case "telecom", "ctcc", "china_telecom":
		return "china_telecom", nil
	case "unicom", "cucc", "china_unicom":
		return "china_unicom", nil
	case "all", "china_all":
		return "china_all", nil
	default:
		return "", fmt.Errorf("line must be mobile, telecom, unicom, or all")
	}
}

func createBucketWithDefaults(c client, args []string, commandName, defaultProfile, defaultCacheControl string) error {
	fs := flag.NewFlagSet(commandName, flag.ExitOnError)
	slug := fs.String("slug", "", "bucket slug")
	name := fs.String("name", "", "bucket display name")
	description := fs.String("description", "", "bucket description")
	profile := fs.String("profile", defaultProfile, "default route profile")
	routingPolicy := fs.String("routing-policy", "", "routing policy name; requires matching multi-source route profile")
	types := fs.String("types", "", "comma-separated asset types: image,video,document,archive,other")
	maxCapacity := fs.Int64("max-capacity", 0, "bucket capacity limit in bytes; 0 means unlimited")
	maxFileSize := fs.Int64("max-file-size", 0, "single file limit in bytes; 0 means unlimited")
	cacheControl := fs.String("cache-control", defaultCacheControl, "default Cache-Control value")
	_ = fs.Parse(args)
	if *slug == "" {
		return errors.New("-slug is required")
	}
	req := map[string]any{
		"slug":                  *slug,
		"name":                  *name,
		"description":           *description,
		"route_profile":         *profile,
		"routing_policy":        strings.TrimSpace(*routingPolicy),
		"allowed_types":         splitCSV(*types),
		"max_capacity_bytes":    *maxCapacity,
		"max_file_size_bytes":   *maxFileSize,
		"default_cache_control": *cacheControl,
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets", req)
}

func initBucket(c client, args []string) error {
	fs := flag.NewFlagSet("init-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	dryRun := fs.Bool("dry-run", false, "return the initialization plan without creating directories")
	_ = fs.Parse(args)
	if *bucket == "" {
		return errors.New("-bucket is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(*bucket)+"/init", map[string]any{"dry_run": *dryRun})
}

func uploadBucket(c client, args []string) error {
	fs := flag.NewFlagSet("upload-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	file := fs.String("file", "", "file to upload")
	dst := fs.String("path", "", "logical path inside the bucket")
	assetType := fs.String("asset-type", "", "optional asset type override")
	cacheControl := fs.String("cache-control", "", "Cache-Control value override")
	warmup := fs.Bool("warmup", false, "warm the uploaded public URL after upload")
	warmupMethod := fs.String("warmup-method", http.MethodHead, "warmup method: HEAD or GET")
	warmupBaseURL := fs.String("warmup-base-url", "", "public base URL override for warmup")
	_ = fs.Parse(args)
	if *bucket == "" || *file == "" || *dst == "" {
		return errors.New("-bucket, -file and -path are required")
	}
	uploadRaw, err := uploadBucketObject(c, *bucket, *file, *dst, *assetType, *cacheControl)
	if err != nil {
		return bucketUploadDiagnosticError(err, *bucket, *dst)
	}
	if !*warmup {
		return printBucketUploadOutput(uploadRaw, nil, *bucket, *dst)
	}
	warmupRaw, err := warmupBucketObject(c, *bucket, *dst, *warmupMethod, *warmupBaseURL)
	if err != nil {
		return fmt.Errorf("upload succeeded but warmup failed: %w; next diagnostic: %s", err, makeCDNDoctorCommand(*bucket, *dst))
	}
	return printBucketUploadOutput(uploadRaw, warmupRaw, *bucket, *dst)
}

func printBucketUploadOutput(uploadRaw, warmupRaw []byte, bucket, logicalPath string) error {
	if len(warmupRaw) == 0 {
		var root map[string]any
		if err := json.Unmarshal(uploadRaw, &root); err != nil {
			return printJSON(uploadRaw)
		}
		enrichBucketUploadObject(root, bucket, logicalPath, "uploaded")
		raw, err := json.Marshal(root)
		if err != nil {
			return err
		}
		return printJSON(raw)
	}
	root := map[string]any{
		"upload":  json.RawMessage(uploadRaw),
		"warmup":  json.RawMessage(warmupRaw),
		"summary": bucketUploadSummary("uploaded and warmed", bucket, logicalPath),
	}
	var uploadFields map[string]any
	if err := json.Unmarshal(uploadRaw, &uploadFields); err == nil {
		if copyURLs := bucketUploadCopyURLs(uploadFields); len(copyURLs) > 0 {
			root["copy_urls"] = copyURLs
		}
	}
	if commands := bucketUploadNextCommands(bucket, logicalPath); len(commands) > 0 {
		root["next_commands"] = commands
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func enrichBucketUploadObject(root map[string]any, bucket, logicalPath, verb string) {
	if root == nil {
		return
	}
	root["summary"] = bucketUploadSummary(verb, bucket, logicalPath)
	if copyURLs := bucketUploadCopyURLs(root); len(copyURLs) > 0 {
		root["copy_urls"] = copyURLs
	}
	root["next_commands"] = appendCommandHints(stringSliceFromAny(root["next_commands"]), bucketUploadNextCommands(bucket, logicalPath)...)
}

func bucketUploadSummary(verb, bucket, logicalPath string) string {
	subject := strings.Trim(strings.TrimSpace(bucket)+"/"+strings.Trim(strings.TrimSpace(logicalPath), "/"), "/")
	if subject == "" {
		subject = strings.TrimSpace(logicalPath)
	}
	if subject == "" {
		subject = strings.TrimSpace(bucket)
	}
	if subject == "" {
		return verb
	}
	return verb + " " + subject
}

func bucketUploadCopyURLs(root map[string]any) map[string]string {
	copyURLs := map[string]string{}
	for _, key := range []string{"public_url", "cdn_url", "storage_url"} {
		if value := stringFieldFromMap(root, key); value != "" {
			copyURLs[key] = value
		}
	}
	if len(copyURLs) == 0 {
		return nil
	}
	return copyURLs
}

func bucketUploadNextCommands(bucket, logicalPath string) []string {
	if cmd := makeCDNDoctorCommand(bucket, logicalPath); cmd != "" {
		return []string{cmd}
	}
	return nil
}

func bucketUploadDiagnosticError(err error, bucket, logicalPath string) error {
	if err == nil {
		return nil
	}
	if cmd := makeCDNDoctorCommand(bucket, logicalPath); cmd != "" {
		return fmt.Errorf("%w; next diagnostic: %s", err, cmd)
	}
	return err
}

func makeCDNDoctorCommand(bucket, logicalPath string) string {
	bucket = strings.TrimSpace(bucket)
	logicalPath = strings.Trim(strings.TrimSpace(logicalPath), "/")
	if bucket == "" {
		return ""
	}
	cmd := "supercdnctl cdn-doctor -bucket " + cliHintArg(bucket)
	if logicalPath != "" {
		cmd += " -path " + cliHintArg(logicalPath)
	}
	return cmd
}

func stringFieldFromMap(root map[string]any, key string) string {
	if root == nil {
		return ""
	}
	value, _ := root[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceFromAny(value any) []string {
	switch items := value.(type) {
	case []string:
		return append([]string(nil), items...)
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func appendCommandHints(existing []string, additions ...string) []string {
	out := make([]string, 0, len(existing)+len(additions))
	seen := map[string]bool{}
	for _, value := range append(existing, additions...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cliHintArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\r\n'\"`$&|;()<>[]{}") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

type bucketDirUploadPlan struct {
	File        string `json:"file"`
	LogicalPath string `json:"path"`
	Size        int64  `json:"size"`
}

type bucketDirUploadResult struct {
	File        string          `json:"file"`
	LogicalPath string          `json:"path"`
	Size        int64           `json:"size"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts,omitempty"`
	Error       string          `json:"error,omitempty"`
	Upload      json.RawMessage `json:"upload,omitempty"`
	Warmup      json.RawMessage `json:"warmup,omitempty"`
}

type bucketDirUploadReport struct {
	Bucket        string                  `json:"bucket"`
	Summary       string                  `json:"summary,omitempty"`
	Dir           string                  `json:"dir"`
	Prefix        string                  `json:"prefix,omitempty"`
	DryRun        bool                    `json:"dry_run,omitempty"`
	ReportFile    string                  `json:"report_file,omitempty"`
	ReportSavedTo string                  `json:"report_saved_to,omitempty"`
	Retry         int                     `json:"retry,omitempty"`
	SkipExisting  bool                    `json:"skip_existing,omitempty"`
	Concurrency   int                     `json:"concurrency"`
	Total         int                     `json:"total"`
	Planned       int                     `json:"planned,omitempty"`
	Succeeded     int                     `json:"succeeded"`
	Skipped       int                     `json:"skipped,omitempty"`
	Failed        int                     `json:"failed"`
	NextCommands  []string                `json:"next_commands,omitempty"`
	Results       []bucketDirUploadResult `json:"results"`
}

type bucketDirUploadJob struct {
	Index int
	Plan  bucketDirUploadPlan
}

func uploadBucketDir(c client, args []string) error {
	fs := flag.NewFlagSet("upload-bucket-dir", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	dir := fs.String("dir", "", "directory to upload")
	prefix := fs.String("prefix", "", "logical path prefix inside the bucket")
	assetType := fs.String("asset-type", "", "optional asset type override for every file")
	cacheControl := fs.String("cache-control", "", "Cache-Control value override for every file")
	concurrency := fs.Int("concurrency", 10, "maximum parallel uploads")
	dryRun := fs.Bool("dry-run", false, "print the upload plan without sending files")
	reportFile := fs.String("report-file", "", "write the JSON report to this file")
	retry := fs.Int("retry", 0, "per-file retry count after the initial attempt")
	skipExisting := fs.Bool("skip-existing", false, "skip files whose logical path already exists in the bucket")
	warmup := fs.Bool("warmup", false, "warm uploaded public URLs after upload")
	warmupMethod := fs.String("warmup-method", http.MethodHead, "warmup method: HEAD or GET")
	warmupBaseURL := fs.String("warmup-base-url", "", "public base URL override for warmup")
	_ = fs.Parse(args)
	if *bucket == "" || *dir == "" {
		return errors.New("-bucket and -dir are required")
	}
	if *concurrency <= 0 {
		return errors.New("-concurrency must be greater than 0")
	}
	if *retry < 0 {
		return errors.New("-retry must be non-negative")
	}
	plans, cleanPrefix, err := planBucketDirUpload(*dir, *prefix)
	if err != nil {
		return err
	}
	report := bucketDirUploadReport{
		Bucket:       *bucket,
		Dir:          *dir,
		Prefix:       cleanPrefix,
		DryRun:       *dryRun,
		ReportFile:   strings.TrimSpace(*reportFile),
		Retry:        *retry,
		SkipExisting: *skipExisting,
		Concurrency:  *concurrency,
		Total:        len(plans),
		Results:      make([]bucketDirUploadResult, len(plans)),
	}
	if len(plans) == 0 {
		return finishBucketDirUploadReport(report, report.ReportFile)
	}
	if *dryRun {
		for i, plan := range plans {
			report.Results[i] = bucketDirUploadResult{
				File:        plan.File,
				LogicalPath: plan.LogicalPath,
				Size:        plan.Size,
				Status:      "planned",
			}
		}
		summarizeBucketDirUploadReport(&report)
		return finishBucketDirUploadReport(report, report.ReportFile)
	}
	existing := map[string]bool{}
	if *skipExisting {
		existing, err = existingBucketDirUploadPaths(c, *bucket, plans)
		if err != nil {
			return err
		}
	}
	uploadJobs := make([]bucketDirUploadJob, 0, len(plans))
	for i, plan := range plans {
		if existing[plan.LogicalPath] {
			report.Results[i] = bucketDirUploadResult{
				File:        plan.File,
				LogicalPath: plan.LogicalPath,
				Size:        plan.Size,
				Status:      "skipped",
			}
			continue
		}
		uploadJobs = append(uploadJobs, bucketDirUploadJob{Index: i, Plan: plan})
	}
	jobs := make(chan bucketDirUploadJob)
	var wg sync.WaitGroup
	workers := *concurrency
	if workers > len(uploadJobs) {
		workers = len(uploadJobs)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				report.Results[job.Index] = uploadBucketDirOne(c, *bucket, job.Plan, *assetType, *cacheControl, *warmup, *warmupMethod, *warmupBaseURL, *retry)
			}
		}()
	}
	for _, job := range uploadJobs {
		jobs <- job
	}
	close(jobs)
	wg.Wait()
	summarizeBucketDirUploadReport(&report)
	reportErr := finishBucketDirUploadReport(report, report.ReportFile)
	if report.Failed > 0 {
		uploadErr := fmt.Errorf("bucket directory upload failed: %d of %d files failed", report.Failed, report.Total)
		if reportErr != nil {
			return fmt.Errorf("%w; additionally failed to write report: %v", uploadErr, reportErr)
		}
		return uploadErr
	}
	return reportErr
}

func summarizeBucketDirUploadReport(report *bucketDirUploadReport) {
	report.Planned = 0
	report.Succeeded = 0
	report.Skipped = 0
	report.Failed = 0
	for _, result := range report.Results {
		switch result.Status {
		case "ok":
			report.Succeeded++
		case "skipped":
			report.Skipped++
		case "planned":
			report.Planned++
		default:
			report.Failed++
		}
	}
}

func finishBucketDirUploadReport(report bucketDirUploadReport, reportFile string) error {
	prepareBucketDirUploadReport(&report, reportFile)
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if printErr := printJSON(raw); printErr != nil {
		return printErr
	}
	if strings.TrimSpace(reportFile) == "" {
		return nil
	}
	return writeJSONReportFile(reportFile, raw)
}

func prepareBucketDirUploadReport(report *bucketDirUploadReport, reportFile string) {
	if report == nil {
		return
	}
	report.ReportSavedTo = strings.TrimSpace(reportFile)
	report.Summary = bucketDirUploadSummary(report)
	report.NextCommands = appendCommandHints(report.NextCommands, bucketDirUploadNextCommands(report)...)
}

func bucketDirUploadSummary(report *bucketDirUploadReport) string {
	if report == nil {
		return ""
	}
	if report.Planned > 0 {
		return fmt.Sprintf("%d total, %d planned, %d failed", report.Total, report.Planned, report.Failed)
	}
	return fmt.Sprintf("%d total, %d succeeded, %d skipped, %d failed", report.Total, report.Succeeded, report.Skipped, report.Failed)
}

func bucketDirUploadNextCommands(report *bucketDirUploadReport) []string {
	if report == nil || strings.TrimSpace(report.Bucket) == "" {
		return nil
	}
	var commands []string
	if path := firstBucketDirResultPath(report, "error"); path != "" {
		commands = append(commands, makeCDNDoctorCommand(report.Bucket, path))
	}
	if report.Failed > 0 {
		commands = append(commands, bucketDirUploadRetryCommand(report))
		return appendCommandHints(nil, commands...)
	}
	if path := firstBucketDirResultPath(report, "ok", "skipped"); path != "" {
		commands = append(commands, makeCDNDoctorCommand(report.Bucket, path))
	}
	if report.Planned > 0 {
		commands = append(commands, bucketDirUploadRunCommand(report))
	}
	return appendCommandHints(nil, commands...)
}

func firstBucketDirResultPath(report *bucketDirUploadReport, statuses ...string) string {
	if report == nil {
		return ""
	}
	want := map[string]bool{}
	for _, status := range statuses {
		want[status] = true
	}
	for _, result := range report.Results {
		if want[result.Status] && strings.TrimSpace(result.LogicalPath) != "" {
			return result.LogicalPath
		}
	}
	return ""
}

func bucketDirUploadRetryCommand(report *bucketDirUploadReport) string {
	if report == nil {
		return ""
	}
	retry := report.Retry
	if retry < 1 {
		retry = 2
	}
	parts := []string{
		"supercdnctl upload-bucket-dir",
		"-bucket " + cliHintArg(report.Bucket),
		"-dir " + cliHintArg(report.Dir),
	}
	if strings.TrimSpace(report.Prefix) != "" {
		parts = append(parts, "-prefix "+cliHintArg(report.Prefix))
	}
	parts = append(parts, "-skip-existing", "-retry "+strconv.Itoa(retry))
	if strings.TrimSpace(report.ReportFile) != "" {
		parts = append(parts, "-report-file "+cliHintArg(report.ReportFile))
	}
	return strings.Join(parts, " ")
}

func bucketDirUploadRunCommand(report *bucketDirUploadReport) string {
	if report == nil {
		return ""
	}
	parts := []string{
		"supercdnctl upload-bucket-dir",
		"-bucket " + cliHintArg(report.Bucket),
		"-dir " + cliHintArg(report.Dir),
	}
	if strings.TrimSpace(report.Prefix) != "" {
		parts = append(parts, "-prefix "+cliHintArg(report.Prefix))
	}
	if strings.TrimSpace(report.ReportFile) != "" {
		parts = append(parts, "-report-file "+cliHintArg(report.ReportFile))
	}
	return strings.Join(parts, " ")
}

func writeJSONReportFile(pathValue string, raw []byte) error {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return nil
	}
	if dir := filepath.Dir(pathValue); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") == nil {
		pretty.WriteByte('\n')
		return os.WriteFile(pathValue, pretty.Bytes(), 0o644)
	}
	raw = append(raw, '\n')
	return os.WriteFile(pathValue, raw, 0o644)
}

func uploadBucketDirOne(c client, bucket string, plan bucketDirUploadPlan, assetType, cacheControl string, warmup bool, warmupMethod, warmupBaseURL string, retry int) bucketDirUploadResult {
	result := bucketDirUploadResult{
		File:        plan.File,
		LogicalPath: plan.LogicalPath,
		Size:        plan.Size,
	}
	maxAttempts := retry + 1
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result.Attempts = attempt
		result.Upload = nil
		result.Warmup = nil
		uploadRaw, err := uploadBucketObject(c, bucket, plan.File, plan.LogicalPath, assetType, cacheControl)
		if err != nil {
			result.Status = "error"
			result.Error = err.Error()
			continue
		}
		result.Upload = json.RawMessage(uploadRaw)
		if warmup {
			warmupRaw, err := warmupBucketObject(c, bucket, plan.LogicalPath, warmupMethod, warmupBaseURL)
			if err != nil {
				result.Status = "error"
				result.Error = "upload succeeded but warmup failed: " + err.Error()
				continue
			}
			result.Warmup = json.RawMessage(warmupRaw)
		}
		result.Status = "ok"
		result.Error = ""
		return result
	}
	return result
}

func existingBucketDirUploadPaths(c client, bucket string, plans []bucketDirUploadPlan) (map[string]bool, error) {
	existing := map[string]bool{}
	checked := map[string]bool{}
	for _, plan := range plans {
		if checked[plan.LogicalPath] {
			continue
		}
		checked[plan.LogicalPath] = true
		q := url.Values{}
		q.Set("prefix", plan.LogicalPath)
		q.Set("limit", "1")
		raw, err := c.doRaw(http.MethodGet, "/api/v1/asset-buckets/"+url.PathEscape(bucket)+"/objects?"+q.Encode(), nil, "")
		if err != nil {
			return nil, err
		}
		var resp struct {
			Objects []struct {
				LogicalPath string `json:"logical_path"`
			} `json:"objects"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		for _, object := range resp.Objects {
			if object.LogicalPath == plan.LogicalPath {
				existing[plan.LogicalPath] = true
				break
			}
		}
	}
	return existing, nil
}

func planBucketDirUpload(dir, prefix string) ([]bucketDirUploadPlan, string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("-dir %q is not a directory", dir)
	}
	cleanPrefix := cleanBucketDirPrefix(prefix)
	var plans []bucketDirUploadPlan
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		logicalPath := rel
		if cleanPrefix != "" {
			logicalPath = urlpath.Join(cleanPrefix, rel)
		}
		logicalPath = strings.TrimPrefix(logicalPath, "/")
		if logicalPath == "" || logicalPath == "." {
			return fmt.Errorf("invalid logical path for %q", path)
		}
		plans = append(plans, bucketDirUploadPlan{
			File:        path,
			LogicalPath: logicalPath,
			Size:        info.Size(),
		})
		return nil
	}); err != nil {
		return nil, "", err
	}
	sort.Slice(plans, func(i, j int) bool {
		return plans[i].LogicalPath < plans[j].LogicalPath
	})
	return plans, cleanPrefix, nil
}

func cleanBucketDirPrefix(prefix string) string {
	prefix = strings.ReplaceAll(strings.TrimSpace(prefix), "\\", "/")
	prefix = strings.Trim(prefix, "/")
	if prefix == "." {
		return ""
	}
	return prefix
}

func uploadBucketObject(c client, bucket, file, dst, assetType, cacheControl string) ([]byte, error) {
	fields := map[string]string{
		"path":          dst,
		"asset_type":    assetType,
		"cache_control": cacheControl,
	}
	apiPath := "/api/v1/asset-buckets/" + url.PathEscape(bucket) + "/objects"
	return c.uploadFileRaw(apiPath, "file", file, fields)
}

func warmupBucketObject(c client, bucket, dst, method, baseURL string) ([]byte, error) {
	return c.doJSONRaw(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(bucket)+"/warmup", map[string]any{
		"path":     dst,
		"method":   method,
		"base_url": baseURL,
	})
}

func listBucket(c client, args []string) error {
	fs := flag.NewFlagSet("list-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	prefix := fs.String("prefix", "", "logical path prefix")
	limit := fs.Int("limit", 100, "max objects to return")
	_ = fs.Parse(args)
	if *bucket == "" {
		return errors.New("-bucket is required")
	}
	path := "/api/v1/asset-buckets/" + url.PathEscape(*bucket) + "/objects?limit=" + url.QueryEscape(fmt.Sprint(*limit))
	if *prefix != "" {
		path += "&prefix=" + url.QueryEscape(*prefix)
	}
	return c.do(http.MethodGet, path, nil, "")
}

func purgeBucket(c client, args []string) error {
	req, bucket, err := parseBucketCacheFlags("purge-bucket", args)
	if err != nil {
		return err
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(bucket)+"/purge", req)
}

func warmupBucket(c client, args []string) error {
	req, bucket, err := parseBucketCacheFlags("warmup-bucket", args)
	if err != nil {
		return err
	}
	return c.doJSON(http.MethodPost, "/api/v1/asset-buckets/"+url.PathEscape(bucket)+"/warmup", req)
}

func parseBucketCacheFlags(name string, args []string) (map[string]any, string, error) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	pathValue := fs.String("path", "", "single logical object path")
	paths := fs.String("paths", "", "comma-separated logical object paths")
	prefix := fs.String("prefix", "", "logical path prefix")
	all := fs.Bool("all", false, "select all tracked objects in the bucket")
	limit := fs.Int("limit", 0, "max objects for prefix selection; 0 lets the server choose")
	baseURL := fs.String("base-url", "", "public base URL override for generated /a/{bucket}/... URLs")
	cfAccount := fs.String("cloudflare-account", "", "Cloudflare account name")
	cfLibrary := fs.String("cloudflare-library", "", "Cloudflare library name")
	dryRun := fs.Bool("dry-run", false, "generate URLs without purging or requesting them")
	method := fs.String("method", "", "warmup method: HEAD or GET")
	_ = fs.Parse(args)
	if *bucket == "" {
		return nil, "", errors.New("-bucket is required")
	}
	req := map[string]any{
		"path":               *pathValue,
		"paths":              splitCSV(*paths),
		"prefix":             *prefix,
		"all":                *all,
		"limit":              *limit,
		"base_url":           *baseURL,
		"cloudflare_account": *cfAccount,
		"cloudflare_library": *cfLibrary,
		"dry_run":            *dryRun,
	}
	if *method != "" {
		req["method"] = *method
	}
	return req, *bucket, nil
}

func deleteBucketObject(c client, args []string) error {
	fs := flag.NewFlagSet("delete-bucket-object", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	dst := fs.String("path", "", "logical path inside the bucket")
	paths := fs.String("paths", "", "comma-separated logical paths inside the bucket")
	prefix := fs.String("prefix", "", "delete objects whose logical path is under this prefix")
	all := fs.Bool("all", false, "delete all tracked objects in the bucket")
	force := fs.Bool("force", false, "required for -prefix or -all")
	deleteRemote := fs.Bool("delete-remote", true, "delete remote replicas before removing local metadata")
	_ = fs.Parse(args)
	if *bucket == "" {
		return errors.New("-bucket is required")
	}
	exactPaths := splitCSV(*paths)
	if strings.TrimSpace(*dst) != "" {
		exactPaths = append([]string{strings.TrimSpace(*dst)}, exactPaths...)
	}
	modes := 0
	if len(exactPaths) > 0 {
		modes++
	}
	if strings.TrimSpace(*prefix) != "" {
		modes++
	}
	if *all {
		modes++
	}
	if modes == 0 {
		return errors.New("one of -path, -paths, -prefix, or -all is required")
	}
	if modes > 1 {
		return errors.New("choose only one of -path/-paths, -prefix, or -all")
	}
	if (strings.TrimSpace(*prefix) != "" || *all) && !*force {
		return errors.New("-force is required for -prefix or -all")
	}
	q := url.Values{}
	for _, item := range exactPaths {
		q.Add("path", item)
	}
	if strings.TrimSpace(*prefix) != "" {
		q.Set("prefix", strings.TrimSpace(*prefix))
	}
	if *all {
		q.Set("all", "true")
	}
	if *force {
		q.Set("force", "true")
	}
	q.Set("delete_remote", fmt.Sprint(*deleteRemote))
	pathValue := "/api/v1/asset-buckets/" + url.PathEscape(*bucket) + "/objects?" + q.Encode()
	return c.do(http.MethodDelete, pathValue, nil, "")
}

func deleteBucket(c client, args []string) error {
	fs := flag.NewFlagSet("delete-bucket", flag.ExitOnError)
	bucket := fs.String("bucket", "", "bucket slug")
	force := fs.Bool("force", false, "delete a non-empty bucket by deleting its tracked objects first")
	deleteObjects := fs.Bool("delete-objects", false, "delete tracked bucket objects before deleting the bucket")
	deleteRemote := fs.Bool("delete-remote", true, "delete remote object replicas before removing local metadata")
	_ = fs.Parse(args)
	if *bucket == "" {
		return errors.New("-bucket is required")
	}
	if *force {
		*deleteObjects = true
	}
	path := "/api/v1/asset-buckets/" + url.PathEscape(*bucket) +
		"?force=" + url.QueryEscape(fmt.Sprint(*force)) +
		"&delete_objects=" + url.QueryEscape(fmt.Sprint(*deleteObjects)) +
		"&delete_remote=" + url.QueryEscape(fmt.Sprint(*deleteRemote))
	return c.do(http.MethodDelete, path, nil, "")
}
