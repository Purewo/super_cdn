package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

func login(c client, profileName string, args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	inviteToken := fs.String("invite-token", "", "invite token")
	tokenName := fs.String("token-name", "", "local token name")
	_ = fs.Parse(args)
	if *inviteToken == "" {
		return errors.New("-invite-token is required")
	}
	raw, err := c.doJSONRaw(http.MethodPost, "/api/v1/auth/accept-invite", map[string]string{
		"invite_token": *inviteToken,
		"token_name":   firstNonEmpty(*tokenName, profileName),
	})
	if err != nil {
		return err
	}
	var resp struct {
		User     any             `json:"user"`
		APIToken string          `json:"api_token"`
		Token    json.RawMessage `json:"token"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.APIToken == "" {
		return errors.New("login response did not include an api token")
	}
	if err := saveCLIProfile(profileName, c.baseURL, resp.APIToken); err != nil {
		return err
	}
	return printJSON(mustJSON(map[string]any{
		"status":  "ok",
		"profile": profileName,
		"server":  c.baseURL,
		"user":    resp.User,
		"token":   json.RawMessage(resp.Token),
	}))
}

func logout(profileName string, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	_ = fs.Parse(args)
	cfg, err := loadCLIConfig()
	if err != nil {
		return err
	}
	delete(cfg.Profiles, profileName)
	if cfg.CurrentProfile == profileName {
		cfg.CurrentProfile = ""
	}
	if err := saveCLIConfig(cfg); err != nil {
		return err
	}
	return printJSON(mustJSON(map[string]any{"status": "ok", "profile": profileName}))
}

func whoami(c client, args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ExitOnError)
	_ = fs.Parse(args)
	return c.do(http.MethodGet, "/api/v1/auth/me", nil, "")
}

func inviteUser(c client, args []string) error {
	fs := flag.NewFlagSet("invite-user", flag.ExitOnError)
	name := fs.String("name", "", "user name")
	role := fs.String("role", "maintainer", "owner, maintainer, or viewer")
	expires := fs.Duration("expires", 7*24*time.Hour, "invite expiration")
	_ = fs.Parse(args)
	if *name == "" {
		return errors.New("-name is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/auth/invites", map[string]any{
		"name":               *name,
		"role":               *role,
		"expires_in_seconds": int64(expires.Seconds()),
	})
}

func listUsers(c client, args []string) error {
	fs := flag.NewFlagSet("list-users", flag.ExitOnError)
	_ = fs.Parse(args)
	return c.do(http.MethodGet, "/api/v1/users", nil, "")
}

func revokeToken(c client, args []string) error {
	fs := flag.NewFlagSet("revoke-token", flag.ExitOnError)
	id := fs.String("id", "", "token id")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-id is required")
	}
	return c.do(http.MethodDelete, "/api/v1/tokens/"+url.PathEscape(*id), nil, "")
}

func createProject(c client, args []string) error {
	fs := flag.NewFlagSet("create-project", flag.ExitOnError)
	id := fs.String("id", "", "project id")
	_ = fs.Parse(args)
	if *id == "" {
		return errors.New("-id is required")
	}
	return c.doJSON(http.MethodPost, "/api/v1/projects", map[string]string{"id": *id})
}

func uploadAsset(c client, args []string) error {
	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	file := fs.String("file", "", "file to upload")
	project := fs.String("project", "", "project id")
	dst := fs.String("path", "", "object path")
	profile := fs.String("profile", "overseas", "route profile")
	cacheControl := fs.String("cache-control", "", "Cache-Control value")
	_ = fs.Parse(args)
	if *file == "" || *project == "" || *dst == "" {
		return errors.New("-file, -project and -path are required")
	}
	info, err := os.Stat(*file)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", *file)
	}
	if err := c.doJSONQuiet(http.MethodPost, "/api/v1/preflight/upload", map[string]any{
		"route_profile":     *profile,
		"total_size":        info.Size(),
		"largest_file_size": info.Size(),
		"batch_file_count":  1,
	}); err != nil {
		return fmt.Errorf("preflight failed: %w", err)
	}
	fields := map[string]string{
		"project_id":    *project,
		"path":          *dst,
		"route_profile": *profile,
		"cache_control": *cacheControl,
	}
	return c.uploadFile("/api/v1/assets", "file", *file, fields)
}
