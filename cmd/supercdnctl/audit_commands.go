package main

import (
	"flag"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func auditLog(c client, args []string) error {
	fs := flag.NewFlagSet("audit-log", flag.ExitOnError)
	limit := fs.Int("limit", 100, "maximum audit events to return")
	workspace := fs.String("workspace", "", "workspace id filter; root only")
	action := fs.String("action", "", "exact audit action filter")
	resource := fs.String("resource", "", "resource substring filter")
	_ = fs.Parse(args)

	q := url.Values{}
	if *limit > 0 {
		q.Set("limit", strconv.Itoa(*limit))
	}
	if strings.TrimSpace(*workspace) != "" {
		q.Set("workspace_id", strings.TrimSpace(*workspace))
	}
	if strings.TrimSpace(*action) != "" {
		q.Set("action", strings.TrimSpace(*action))
	}
	if strings.TrimSpace(*resource) != "" {
		q.Set("resource", strings.TrimSpace(*resource))
	}
	path := "/api/v1/audit-events"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.do(http.MethodGet, path, nil, "")
}
