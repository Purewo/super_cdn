package main

import (
	"errors"
	"flag"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func gc(c client, args []string) error {
	fs := flag.NewFlagSet("gc", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", true, "plan cleanup without deleting; pass -dry-run=false to delete")
	bucket := fs.String("bucket", "", "bucket slug scope for future remote cleanup")
	site := fs.String("site", "", "site id scope for future remote cleanup")
	olderThan := fs.Duration("older-than", time.Hour, "only clean local staging files older than this duration")
	deleteRemote := fs.Bool("delete-remote", false, "allow future remote cleanup; current implementation only cleans local staging")
	force := fs.Bool("force", false, "required for very small -older-than values")
	_ = fs.Parse(args)
	if strings.TrimSpace(*bucket) != "" && strings.TrimSpace(*site) != "" {
		return errors.New("choose only one of -bucket or -site")
	}
	if *olderThan <= 0 {
		return errors.New("-older-than must be greater than 0")
	}
	return c.doJSON(http.MethodPost, "/api/v1/gc", map[string]any{
		"dry_run":            *dryRun,
		"bucket":             strings.TrimSpace(*bucket),
		"site":               strings.TrimSpace(*site),
		"older_than_seconds": int64(olderThan.Seconds()),
		"delete_remote":      *deleteRemote,
		"force":              *force,
	})
}

func gcSite(c client, args []string) error {
	fs := flag.NewFlagSet("gc-site", flag.ExitOnError)
	site := fs.String("site", "", "site id")
	_ = fs.Parse(args)
	if *site == "" {
		return errors.New("-site is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/sites/"+url.PathEscape(*site)+"/gc", map[string]any{})
}
