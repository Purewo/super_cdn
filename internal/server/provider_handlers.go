package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"supercdn/internal/cloudflare"
	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type syncCloudflareR2Request struct {
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	All               bool     `json:"all"`
	DryRun            *bool    `json:"dry_run,omitempty"`
	Force             bool     `json:"force"`
	SyncCORS          *bool    `json:"sync_cors,omitempty"`
	SyncDomain        *bool    `json:"sync_domain,omitempty"`
	CORSOrigins       []string `json:"cors_origins"`
	CORSMethods       []string `json:"cors_methods"`
	CORSHeaders       []string `json:"cors_headers"`
	CORSExposeHeaders []string `json:"cors_expose_headers"`
	CORSMaxAgeSeconds int      `json:"cors_max_age_seconds"`
}

type syncCloudflareR2Response struct {
	DryRun   bool                            `json:"dry_run"`
	Force    bool                            `json:"force"`
	Status   string                          `json:"status"`
	Accounts []syncCloudflareR2AccountResult `json:"accounts"`
	Warnings []string                        `json:"warnings,omitempty"`
	Errors   []string                        `json:"errors,omitempty"`
}

type syncCloudflareR2AccountResult struct {
	Account       string                  `json:"account"`
	Default       bool                    `json:"default"`
	Library       string                  `json:"library,omitempty"`
	Bucket        string                  `json:"bucket,omitempty"`
	PublicBaseURL string                  `json:"public_base_url,omitempty"`
	Result        cloudflare.R2SyncResult `json:"result"`
}

type provisionCloudflareR2Request struct {
	CloudflareAccount string   `json:"cloudflare_account"`
	CloudflareLibrary string   `json:"cloudflare_library"`
	All               bool     `json:"all"`
	Bucket            string   `json:"bucket"`
	PublicBaseURL     string   `json:"public_base_url"`
	LocationHint      string   `json:"location_hint"`
	Jurisdiction      string   `json:"jurisdiction"`
	StorageClass      string   `json:"storage_class"`
	DryRun            *bool    `json:"dry_run,omitempty"`
	Force             bool     `json:"force"`
	SyncCORS          *bool    `json:"sync_cors,omitempty"`
	SyncDomain        *bool    `json:"sync_domain,omitempty"`
	CORSOrigins       []string `json:"cors_origins"`
	CORSMethods       []string `json:"cors_methods"`
	CORSHeaders       []string `json:"cors_headers"`
	CORSExposeHeaders []string `json:"cors_expose_headers"`
	CORSMaxAgeSeconds int      `json:"cors_max_age_seconds"`
}

type provisionCloudflareR2Response struct {
	DryRun   bool                                 `json:"dry_run"`
	Force    bool                                 `json:"force"`
	Status   string                               `json:"status"`
	Accounts []provisionCloudflareR2AccountResult `json:"accounts"`
	Warnings []string                             `json:"warnings,omitempty"`
	Errors   []string                             `json:"errors,omitempty"`
}

type provisionCloudflareR2AccountResult struct {
	Account       string                       `json:"account"`
	Default       bool                         `json:"default"`
	Library       string                       `json:"library,omitempty"`
	Bucket        string                       `json:"bucket,omitempty"`
	PublicBaseURL string                       `json:"public_base_url,omitempty"`
	Result        cloudflare.R2ProvisionResult `json:"result"`
}

type createCloudflareR2CredentialsRequest struct {
	CloudflareAccount   string `json:"cloudflare_account"`
	CloudflareLibrary   string `json:"cloudflare_library"`
	All                 bool   `json:"all"`
	Bucket              string `json:"bucket"`
	Jurisdiction        string `json:"jurisdiction"`
	TokenName           string `json:"token_name"`
	PermissionGroupName string `json:"permission_group_name"`
	DryRun              *bool  `json:"dry_run,omitempty"`
	Force               bool   `json:"force"`
}

type createCloudflareR2CredentialsResponse struct {
	DryRun   bool                                         `json:"dry_run"`
	Force    bool                                         `json:"force"`
	Status   string                                       `json:"status"`
	Accounts []createCloudflareR2CredentialsAccountResult `json:"accounts"`
	Warnings []string                                     `json:"warnings,omitempty"`
	Errors   []string                                     `json:"errors,omitempty"`
}

type createCloudflareR2CredentialsAccountResult struct {
	Account       string                         `json:"account"`
	Default       bool                           `json:"default"`
	Library       string                         `json:"library,omitempty"`
	Bucket        string                         `json:"bucket,omitempty"`
	PublicBaseURL string                         `json:"public_base_url,omitempty"`
	Result        cloudflare.R2CredentialsResult `json:"result"`
}

type refreshIPFSPinsRequest struct {
	ObjectID int64  `json:"object_id"`
	Target   string `json:"target,omitempty"`
}

type refreshIPFSPinsResponse struct {
	Status   string          `json:"status"`
	ObjectID int64           `json:"object_id"`
	Target   string          `json:"target,omitempty"`
	Pins     []model.IPFSPin `json:"pins,omitempty"`
	Errors   []string        `json:"errors,omitempty"`
}

type cloudflareR2SyncTarget struct {
	Account config.CloudflareAccountConfig
	Library string
}

func (s *Server) handleCloudflareStatus(w http.ResponseWriter, r *http.Request) {
	accountName := strings.TrimSpace(r.URL.Query().Get("account"))
	if r.URL.Query().Get("all") == "true" {
		accounts := s.cfg.CloudflareAccountsEffective()
		statuses := make([]map[string]any, 0, len(accounts))
		for _, account := range accounts {
			statuses = append(statuses, map[string]any{
				"account": account.Name,
				"default": account.Default,
				"status":  s.cloudflareStatusForAccount(r.Context(), account),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"accounts":  statuses,
			"libraries": s.cfg.CloudflareLibrariesEffective(),
		})
		return
	}
	if accountName != "" {
		account, ok := s.cfg.CloudflareAccountByName(accountName)
		if !ok {
			writeError(w, http.StatusNotFound, "cloudflare account not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"account": account.Name,
			"default": account.Default,
			"status":  s.cloudflareStatusForAccount(r.Context(), account),
		})
		return
	}
	if account, ok := s.cfg.DefaultCloudflareAccount(); ok {
		writeJSON(w, http.StatusOK, s.cloudflareStatusForAccount(r.Context(), account))
		return
	}
	writeJSON(w, http.StatusOK, s.cloudflareClient().Status(r.Context()))
}

func (s *Server) handleIPFSStatus(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	names := s.stores.Names()
	sort.Strings(names)
	providers := make([]storage.ProviderStatus, 0)
	for _, name := range names {
		store, ok := s.stores.Get(name)
		if !ok {
			continue
		}
		if target != "" && name != target {
			continue
		}
		if store.Type() != "pinata" {
			if target != "" {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("storage target %q is %q, not an IPFS provider", target, store.Type()))
				return
			}
			continue
		}
		statuser, ok := store.(storage.ProviderStatusStore)
		if !ok {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("storage target %q does not expose provider status", name))
			return
		}
		providers = append(providers, statuser.ProviderStatus(r.Context()))
	}
	if target != "" && len(providers) == 0 {
		if _, ok := s.stores.Get(target); ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("storage target %q is not an IPFS provider", target))
			return
		}
		writeError(w, http.StatusNotFound, "ipfs provider not found")
		return
	}
	ok := len(providers) > 0
	for _, provider := range providers {
		ok = ok && provider.OK
	}
	resp := map[string]any{
		"configured": len(providers) > 0,
		"ok":         ok,
		"providers":  providers,
	}
	if len(providers) == 0 {
		resp["warnings"] = []string{"no IPFS/Pinata storage target is configured"}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRefreshIPFSPins(w http.ResponseWriter, r *http.Request) {
	var req refreshIPFSPinsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	resp, err := s.refreshIPFSPins(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusOK
	if resp.Status == "partial" || resp.Status == "failed" {
		status = http.StatusBadGateway
	}
	if !s.auditMutation(w, r, "ipfs.pins.refresh", "target:"+strings.TrimSpace(req.Target)) {
		return
	}
	writeJSON(w, status, resp)
}

func (s *Server) refreshIPFSPins(ctx context.Context, req refreshIPFSPinsRequest) (refreshIPFSPinsResponse, error) {
	if req.ObjectID <= 0 {
		return refreshIPFSPinsResponse{}, fmt.Errorf("object_id is required")
	}
	pins, err := s.db.IPFSPins(ctx, req.ObjectID)
	if err != nil {
		return refreshIPFSPinsResponse{}, err
	}
	target := strings.TrimSpace(req.Target)
	resp := refreshIPFSPinsResponse{
		Status:   "ok",
		ObjectID: req.ObjectID,
		Target:   target,
	}
	for _, pin := range pins {
		if target != "" && pin.Target != target {
			continue
		}
		store, ok := s.stores.Get(pin.Target)
		if !ok {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: storage target is not configured", pin.Target))
			continue
		}
		refresher, ok := store.(storage.IPFSPinStatusStore)
		if !ok {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s: storage target does not support IPFS pin refresh", pin.Target))
			continue
		}
		status, err := refresher.RefreshIPFSPin(ctx, pin.CID)
		if err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: %s", pin.Target, pin.CID, err.Error()))
			continue
		}
		updated := pin
		updated.Provider = firstNonEmpty(status.Provider, pin.Provider)
		updated.CID = firstNonEmpty(status.CID, pin.CID)
		updated.GatewayURL = firstNonEmpty(status.GatewayURL, pin.GatewayURL)
		updated.Locator = storage.PreserveIPFSProviderQuery(firstNonEmpty(status.Locator, pin.Locator), pin.Locator)
		updated.PinStatus = firstNonEmpty(status.PinStatus, pin.PinStatus)
		updated.ProviderPinID = firstNonEmpty(status.ProviderPinID, pin.ProviderPinID)
		saved, err := s.db.UpsertIPFSPin(ctx, updated)
		if err != nil {
			resp.Errors = append(resp.Errors, fmt.Sprintf("%s/%s: %s", pin.Target, pin.CID, err.Error()))
			continue
		}
		resp.Pins = append(resp.Pins, *saved)
	}
	if target != "" && len(resp.Pins) == 0 && len(resp.Errors) == 0 {
		return refreshIPFSPinsResponse{}, fmt.Errorf("ipfs pin for object %d target %q not found", req.ObjectID, target)
	}
	if len(resp.Pins) == 0 && len(resp.Errors) == 0 {
		return refreshIPFSPinsResponse{}, fmt.Errorf("object %d has no IPFS pins", req.ObjectID)
	}
	if len(resp.Errors) > 0 {
		if len(resp.Pins) > 0 {
			resp.Status = "partial"
		} else {
			resp.Status = "failed"
		}
	}
	return resp, nil
}

func (s *Server) handleSyncCloudflareR2(w http.ResponseWriter, r *http.Request) {
	var req syncCloudflareR2Request
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	resp := s.syncCloudflareR2(r.Context(), req)
	if !s.auditMutation(w, r, "cloudflare.r2.sync", "cloudflare_r2:"+strings.TrimSpace(req.CloudflareAccount)) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleProvisionCloudflareR2(w http.ResponseWriter, r *http.Request) {
	var req provisionCloudflareR2Request
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	resp := s.provisionCloudflareR2(r.Context(), req)
	if !s.auditMutation(w, r, "cloudflare.r2.provision", "cloudflare_r2:"+strings.TrimSpace(req.CloudflareAccount)) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateCloudflareR2Credentials(w http.ResponseWriter, r *http.Request) {
	var req createCloudflareR2CredentialsRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	resp := s.createCloudflareR2Credentials(r.Context(), req)
	if !s.auditMutation(w, r, "cloudflare.r2.credentials.create", "cloudflare_r2:"+strings.TrimSpace(req.CloudflareAccount)) {
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
