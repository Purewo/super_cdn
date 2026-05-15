package main

import (
	"errors"
	"flag"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func quota(c client, args []string) error {
	fs := flag.NewFlagSet("quota", flag.ExitOnError)
	_ = fs.Parse(args)
	return c.do(http.MethodGet, "/api/v1/quota", nil, "")
}

func requestQuota(c client, args []string) error {
	fs := flag.NewFlagSet("request-quota", flag.ExitOnError)
	maxBytes := fs.Int64("max-bytes", 0, "requested total upload quota in bytes")
	maxGB := fs.Float64("max-gb", 0, "requested total upload quota in GiB")
	reason := fs.String("reason", "", "quota request reason")
	_ = fs.Parse(args)
	requested := quotaBytesFromFlags(*maxBytes, *maxGB)
	if requested <= 0 {
		return errors.New("one of -max-bytes or -max-gb is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/quota/requests", map[string]any{
		"requested_max_bytes": requested,
		"reason":              *reason,
	})
}

func quotaRequests(c client, args []string) error {
	fs := flag.NewFlagSet("quota-requests", flag.ExitOnError)
	status := fs.String("status", "", "pending, approved, or rejected")
	userID := fs.Int64("user-id", 0, "filter by user id")
	limit := fs.Int("limit", 100, "maximum requests to return")
	workspaceID := fs.String("workspace", "", "root-only workspace filter")
	_ = fs.Parse(args)
	q := url.Values{}
	if strings.TrimSpace(*status) != "" {
		q.Set("status", strings.TrimSpace(*status))
	}
	if *userID > 0 {
		q.Set("user_id", strconv.FormatInt(*userID, 10))
	}
	if *limit > 0 {
		q.Set("limit", strconv.Itoa(*limit))
	}
	if strings.TrimSpace(*workspaceID) != "" {
		q.Set("workspace_id", strings.TrimSpace(*workspaceID))
	}
	path := "/api/v1/quota/requests"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.do(http.MethodGet, path, nil, "")
}

func approveQuota(c client, args []string) error {
	fs := flag.NewFlagSet("approve-quota", flag.ExitOnError)
	id := fs.String("id", "", "quota request id")
	maxBytes := fs.Int64("max-bytes", 0, "approved total upload quota in bytes")
	maxGB := fs.Float64("max-gb", 0, "approved total upload quota in GiB")
	note := fs.String("note", "", "approval note")
	workspaceID := fs.String("workspace", "", "workspace id")
	_ = fs.Parse(args)
	if strings.TrimSpace(*id) == "" {
		return errors.New("-id is required")
	}
	body := map[string]any{
		"approved_max_bytes": quotaBytesFromFlags(*maxBytes, *maxGB),
		"note":               *note,
	}
	if strings.TrimSpace(*workspaceID) != "" {
		body["workspace_id"] = strings.TrimSpace(*workspaceID)
	}
	return c.doJSON(http.MethodPost, "/api/v1/quota/requests/"+url.PathEscape(*id)+"/approve", body)
}

func rejectQuota(c client, args []string) error {
	fs := flag.NewFlagSet("reject-quota", flag.ExitOnError)
	id := fs.String("id", "", "quota request id")
	note := fs.String("note", "", "rejection note")
	workspaceID := fs.String("workspace", "", "workspace id")
	_ = fs.Parse(args)
	if strings.TrimSpace(*id) == "" {
		return errors.New("-id is required")
	}
	body := map[string]any{"note": *note}
	if strings.TrimSpace(*workspaceID) != "" {
		body["workspace_id"] = strings.TrimSpace(*workspaceID)
	}
	return c.doJSON(http.MethodPost, "/api/v1/quota/requests/"+url.PathEscape(*id)+"/reject", body)
}

func setUserQuota(c client, args []string) error {
	fs := flag.NewFlagSet("set-user-quota", flag.ExitOnError)
	userID := fs.Int64("user-id", 0, "user id")
	maxBytes := fs.Int64("max-bytes", 0, "total upload quota in bytes")
	maxGB := fs.Float64("max-gb", 0, "total upload quota in GiB")
	note := fs.String("note", "", "change note")
	workspaceID := fs.String("workspace", "", "workspace id")
	_ = fs.Parse(args)
	if *userID <= 0 {
		return errors.New("-user-id is required")
	}
	max := quotaBytesFromFlags(*maxBytes, *maxGB)
	if max <= 0 {
		return errors.New("one of -max-bytes or -max-gb is required")
	}
	body := map[string]any{
		"max_bytes": max,
		"note":      *note,
	}
	if strings.TrimSpace(*workspaceID) != "" {
		body["workspace_id"] = strings.TrimSpace(*workspaceID)
	}
	return c.doJSON(http.MethodPost, "/api/v1/quota/users/"+strconv.FormatInt(*userID, 10), body)
}

func quotaBytesFromFlags(maxBytes int64, maxGB float64) int64 {
	if maxBytes > 0 {
		return maxBytes
	}
	if maxGB <= 0 {
		return 0
	}
	value := maxGB * 1024 * 1024 * 1024
	const maxInt64 = int64(^uint64(0) >> 1)
	if value > float64(maxInt64) {
		return maxInt64
	}
	return int64(value)
}
