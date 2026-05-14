package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"supercdn/internal/cloudflare"
	"supercdn/internal/config"
)

func (s *Server) syncCloudflareR2(ctx context.Context, req syncCloudflareR2Request) syncCloudflareR2Response {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	syncCORS := true
	if req.SyncCORS != nil {
		syncCORS = *req.SyncCORS
	}
	syncDomain := true
	if req.SyncDomain != nil {
		syncDomain = *req.SyncDomain
	}
	resp := syncCloudflareR2Response{DryRun: dryRun, Force: req.Force, Status: "ok"}
	targets, warnings, err := s.cloudflareR2SyncTargets(req)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	for _, target := range targets {
		account := target.Account
		result := s.cloudflareR2ClientForAccount(account).SyncR2Bucket(ctx, cloudflare.SyncR2Options{
			Bucket:             account.R2.Bucket,
			PublicBaseURL:      account.R2.PublicBaseURL,
			ZoneID:             account.ZoneID,
			DryRun:             dryRun,
			Force:              req.Force,
			SyncCORS:           syncCORS,
			SyncDomain:         syncDomain,
			CORSAllowedOrigins: req.CORSOrigins,
			CORSAllowedMethods: req.CORSMethods,
			CORSAllowedHeaders: req.CORSHeaders,
			CORSExposeHeaders:  req.CORSExposeHeaders,
			CORSMaxAgeSeconds:  req.CORSMaxAgeSeconds,
		})
		resp.Accounts = append(resp.Accounts, syncCloudflareR2AccountResult{
			Account:       account.Name,
			Default:       account.Default,
			Library:       target.Library,
			Bucket:        account.R2.Bucket,
			PublicBaseURL: account.R2.PublicBaseURL,
			Result:        result,
		})
		if result.Status == "planned" && resp.Status == "ok" {
			resp.Status = "planned"
		}
		if result.Status == "partial" || result.Status == "failed" {
			if resp.Status != "failed" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, strings.Join(result.Errors, "; ")))
		}
	}
	if len(resp.Accounts) == 0 {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "no cloudflare accounts with r2 config selected")
	}
	return resp
}

func (s *Server) provisionCloudflareR2(ctx context.Context, req provisionCloudflareR2Request) provisionCloudflareR2Response {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	syncCORS := true
	if req.SyncCORS != nil {
		syncCORS = *req.SyncCORS
	}
	syncDomain := true
	if req.SyncDomain != nil {
		syncDomain = *req.SyncDomain
	}
	resp := provisionCloudflareR2Response{DryRun: dryRun, Force: req.Force, Status: "ok"}
	targets, warnings, err := s.cloudflareR2ProvisionTargets(req)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	for _, target := range targets {
		account := target.Account
		bucket := s.cloudflareR2ProvisionBucket(req, target, len(targets) > 1)
		publicBaseURL, publicWarnings := s.cloudflareR2ProvisionPublicBaseURL(req, target, len(targets) > 1)
		resp.Warnings = append(resp.Warnings, publicWarnings...)
		result := s.cloudflareR2ClientForAccount(account).ProvisionR2Bucket(ctx, cloudflare.R2ProvisionOptions{
			Bucket:             bucket,
			PublicBaseURL:      publicBaseURL,
			ZoneID:             account.ZoneID,
			LocationHint:       req.LocationHint,
			Jurisdiction:       req.Jurisdiction,
			StorageClass:       req.StorageClass,
			DryRun:             dryRun,
			Force:              req.Force,
			SyncCORS:           syncCORS,
			SyncDomain:         syncDomain,
			CORSAllowedOrigins: req.CORSOrigins,
			CORSAllowedMethods: req.CORSMethods,
			CORSAllowedHeaders: req.CORSHeaders,
			CORSExposeHeaders:  req.CORSExposeHeaders,
			CORSMaxAgeSeconds:  req.CORSMaxAgeSeconds,
		})
		resp.Accounts = append(resp.Accounts, provisionCloudflareR2AccountResult{
			Account:       account.Name,
			Default:       account.Default,
			Library:       target.Library,
			Bucket:        bucket,
			PublicBaseURL: publicBaseURL,
			Result:        result,
		})
		if result.Status == "planned" && resp.Status == "ok" {
			resp.Status = "planned"
		}
		if result.Status == "partial" || result.Status == "failed" {
			if resp.Status != "failed" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, strings.Join(result.Errors, "; ")))
		}
	}
	if len(resp.Accounts) == 0 {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "no cloudflare accounts selected")
	}
	return resp
}

func (s *Server) createCloudflareR2Credentials(ctx context.Context, req createCloudflareR2CredentialsRequest) createCloudflareR2CredentialsResponse {
	dryRun := true
	if req.DryRun != nil {
		dryRun = *req.DryRun
	}
	resp := createCloudflareR2CredentialsResponse{DryRun: dryRun, Force: req.Force, Status: "ok"}
	targets, warnings, err := s.cloudflareR2CredentialTargets(req)
	resp.Warnings = append(resp.Warnings, warnings...)
	if err != nil {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, err.Error())
		return resp
	}
	for _, target := range targets {
		account := target.Account
		bucket := s.cloudflareR2CredentialBucket(req, target, len(targets) > 1)
		publicBaseURL, publicWarnings := s.cloudflareR2CredentialPublicBaseURL(target)
		resp.Warnings = append(resp.Warnings, publicWarnings...)
		if !req.Force && account.R2.AccessKeyID != "" && account.R2.SecretAccessKey != "" {
			result := cloudflare.R2CredentialsResult{
				Bucket:              bucket,
				Jurisdiction:        normalizeR2CredentialJurisdiction(req.Jurisdiction),
				TokenName:           expandCloudflareProvisionTemplate(req.TokenName, target),
				PermissionGroupName: req.PermissionGroupName,
				DryRun:              dryRun,
				Action:              "skipped",
				Status:              "skipped",
				Error:               "r2 credentials already exist; pass force to create a replacement",
			}
			resp.Accounts = append(resp.Accounts, createCloudflareR2CredentialsAccountResult{
				Account:       account.Name,
				Default:       account.Default,
				Library:       target.Library,
				Bucket:        bucket,
				PublicBaseURL: publicBaseURL,
				Result:        result,
			})
			if resp.Status == "ok" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, result.Error))
			continue
		}
		tokenName := expandCloudflareProvisionTemplate(req.TokenName, target)
		if strings.TrimSpace(tokenName) == "" {
			tokenName = defaultR2CredentialTokenName(account.Name, bucket)
		}
		result := s.cloudflareClientForAccount(account).CreateR2Credentials(ctx, cloudflare.R2CredentialsOptions{
			Bucket:              bucket,
			Jurisdiction:        req.Jurisdiction,
			TokenName:           tokenName,
			PermissionGroupName: req.PermissionGroupName,
			DryRun:              dryRun,
		})
		resp.Accounts = append(resp.Accounts, createCloudflareR2CredentialsAccountResult{
			Account:       account.Name,
			Default:       account.Default,
			Library:       target.Library,
			Bucket:        bucket,
			PublicBaseURL: publicBaseURL,
			Result:        result,
		})
		if result.Status == "planned" && resp.Status == "ok" {
			resp.Status = "planned"
		}
		if result.Status == "failed" || result.Status == "skipped" {
			if resp.Status != "failed" {
				resp.Status = "partial"
			}
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: %s", account.Name, result.Error))
		}
	}
	if len(resp.Accounts) == 0 {
		resp.Status = "failed"
		resp.Errors = append(resp.Errors, "no cloudflare accounts selected")
	}
	return resp
}

func (s *Server) cloudflareR2CredentialTargets(req createCloudflareR2CredentialsRequest) ([]cloudflareR2SyncTarget, []string, error) {
	return s.cloudflareR2ProvisionTargets(provisionCloudflareR2Request{
		CloudflareAccount: req.CloudflareAccount,
		CloudflareLibrary: req.CloudflareLibrary,
		All:               req.All,
		Bucket:            req.Bucket,
		DryRun:            req.DryRun,
	})
}

func (s *Server) cloudflareR2CredentialBucket(req createCloudflareR2CredentialsRequest, target cloudflareR2SyncTarget, multi bool) string {
	return s.cloudflareR2ProvisionBucket(provisionCloudflareR2Request{
		Bucket: req.Bucket,
	}, target, multi)
}

func (s *Server) cloudflareR2CredentialPublicBaseURL(target cloudflareR2SyncTarget) (string, []string) {
	if strings.TrimSpace(target.Account.R2.PublicBaseURL) != "" {
		return normalizeProvisionPublicBaseURL(target.Account.R2.PublicBaseURL), nil
	}
	return s.cloudflareR2ProvisionPublicBaseURL(provisionCloudflareR2Request{}, target, false)
}

func normalizeR2CredentialJurisdiction(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return v
}

func defaultR2CredentialTokenName(accountName, bucket string) string {
	account := cleanDomainLabel(accountName)
	if account == "" {
		account = "account"
	}
	name := cleanDomainLabel(bucket)
	if name == "" {
		name = "bucket"
	}
	return "supercdn-r2-" + account + "-" + name + "-" + time.Now().UTC().Format("20060102T150405Z")
}

func (s *Server) cloudflareR2ProvisionTargets(req provisionCloudflareR2Request) ([]cloudflareR2SyncTarget, []string, error) {
	var warnings []string
	seen := map[string]bool{}
	add := func(account config.CloudflareAccountConfig, library string, out *[]cloudflareR2SyncTarget) {
		if seen[account.Name] {
			return
		}
		seen[account.Name] = true
		*out = append(*out, cloudflareR2SyncTarget{Account: account, Library: library})
	}
	var targets []cloudflareR2SyncTarget
	if strings.TrimSpace(req.CloudflareAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(req.CloudflareAccount)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare account not found")
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, req.CloudflareLibrary)
		add(account, library.Name, &targets)
		return targets, warnings, nil
	}
	if strings.TrimSpace(req.CloudflareLibrary) != "" {
		library, ok := s.cfg.CloudflareLibraryByName(req.CloudflareLibrary)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare library not found")
		}
		for _, binding := range library.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				add(account, library.Name, &targets)
			} else {
				warnings = append(warnings, fmt.Sprintf("cloudflare library %q references missing account %q; skipped", library.Name, binding.Account))
			}
		}
		return targets, warnings, nil
	}
	if req.All {
		for _, account := range s.cfg.CloudflareAccountsEffective() {
			library, _ := s.cloudflareLibraryForAccount(account.Name, "")
			add(account, library.Name, &targets)
		}
		return targets, warnings, nil
	}
	account, ok := s.cfg.DefaultCloudflareAccount()
	if !ok {
		return nil, warnings, fmt.Errorf("cloudflare account is not configured")
	}
	library, _ := s.cloudflareLibraryForAccount(account.Name, "")
	add(account, library.Name, &targets)
	return targets, warnings, nil
}

func (s *Server) cloudflareR2ProvisionBucket(req provisionCloudflareR2Request, target cloudflareR2SyncTarget, multi bool) string {
	account := target.Account
	if strings.TrimSpace(req.Bucket) != "" {
		return cleanR2BucketName(expandCloudflareProvisionTemplate(req.Bucket, target))
	}
	if strings.TrimSpace(account.R2.Bucket) != "" {
		return cleanR2BucketName(account.R2.Bucket)
	}
	name := cloudflareProvisionResourceName(target, multi)
	return cleanR2BucketName("supercdn-" + name)
}

func (s *Server) cloudflareR2ProvisionPublicBaseURL(req provisionCloudflareR2Request, target cloudflareR2SyncTarget, multi bool) (string, []string) {
	account := target.Account
	var warnings []string
	raw := strings.TrimSpace(req.PublicBaseURL)
	if raw != "" {
		return normalizeProvisionPublicBaseURL(expandCloudflareProvisionTemplate(raw, target)), nil
	}
	if strings.TrimSpace(account.R2.PublicBaseURL) != "" {
		return normalizeProvisionPublicBaseURL(account.R2.PublicBaseURL), nil
	}
	root := cleanHost(account.RootDomain)
	if root == "" {
		warnings = append(warnings, fmt.Sprintf("cloudflare account %q has no root_domain; r2 public domain was not planned", account.Name))
		return "", warnings
	}
	label := cloudflareProvisionResourceName(target, multi)
	return "https://" + label + ".r2." + root, warnings
}

func cloudflareProvisionResourceName(target cloudflareR2SyncTarget, multi bool) string {
	base := target.Library
	if base == "" {
		base = target.Account.Name
	}
	base = cleanDomainLabel(base)
	account := cleanDomainLabel(target.Account.Name)
	if base == "" {
		base = "resource"
	}
	if multi && account != "" && !strings.Contains(base, account) {
		base = base + "-" + account
	}
	if len(base) > 63 {
		base = strings.Trim(base[:63], "-")
	}
	return base
}

func expandCloudflareProvisionTemplate(v string, target cloudflareR2SyncTarget) string {
	v = strings.ReplaceAll(v, "{account}", cleanDomainLabel(target.Account.Name))
	v = strings.ReplaceAll(v, "{library}", cleanDomainLabel(target.Library))
	v = strings.ReplaceAll(v, "{root}", cleanHost(target.Account.RootDomain))
	return v
}

func cleanR2BucketName(v string) string {
	v = cleanDomainLabel(v)
	if len(v) > 63 {
		v = strings.Trim(v[:63], "-")
	}
	return v
}

func normalizeProvisionPublicBaseURL(v string) string {
	v = strings.TrimRight(strings.TrimSpace(v), "/")
	if v == "" {
		return ""
	}
	parsed, err := url.Parse(v)
	if err == nil && parsed.Scheme != "" && parsed.Hostname() != "" {
		return v
	}
	return "https://" + strings.TrimPrefix(v, "//")
}

func (s *Server) cloudflareR2SyncTargets(req syncCloudflareR2Request) ([]cloudflareR2SyncTarget, []string, error) {
	var warnings []string
	seen := map[string]bool{}
	add := func(account config.CloudflareAccountConfig, library string, out *[]cloudflareR2SyncTarget) {
		if seen[account.Name] {
			return
		}
		seen[account.Name] = true
		if strings.TrimSpace(account.R2.Bucket) == "" || strings.TrimSpace(account.R2.AccessKeyID) == "" || strings.TrimSpace(account.R2.SecretAccessKey) == "" {
			warnings = append(warnings, fmt.Sprintf("cloudflare account %q has no complete r2 config; skipped", account.Name))
			return
		}
		*out = append(*out, cloudflareR2SyncTarget{Account: account, Library: library})
	}
	var targets []cloudflareR2SyncTarget
	if strings.TrimSpace(req.CloudflareAccount) != "" {
		account, ok := s.cfg.CloudflareAccountByName(req.CloudflareAccount)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare account not found")
		}
		library, _ := s.cloudflareLibraryForAccount(account.Name, req.CloudflareLibrary)
		add(account, library.Name, &targets)
		return targets, warnings, nil
	}
	if strings.TrimSpace(req.CloudflareLibrary) != "" {
		library, ok := s.cfg.CloudflareLibraryByName(req.CloudflareLibrary)
		if !ok {
			return nil, warnings, fmt.Errorf("cloudflare library not found")
		}
		for _, binding := range library.Bindings {
			if account, ok := s.cfg.CloudflareAccountByName(binding.Account); ok {
				add(account, library.Name, &targets)
			}
		}
		return targets, warnings, nil
	}
	if req.All {
		for _, account := range s.cfg.CloudflareAccountsEffective() {
			library, _ := s.cloudflareLibraryForAccount(account.Name, "")
			add(account, library.Name, &targets)
		}
		return targets, warnings, nil
	}
	account, ok := s.cfg.DefaultCloudflareAccount()
	if !ok {
		return nil, warnings, fmt.Errorf("cloudflare account is not configured")
	}
	library, _ := s.cloudflareLibraryForAccount(account.Name, "")
	add(account, library.Name, &targets)
	return targets, warnings, nil
}
