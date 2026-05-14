package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"supercdn/internal/model"
)

type authContextKey struct{}

type authPrincipal struct {
	Root        bool   `json:"root"`
	UserID      int64  `json:"user_id,omitempty"`
	UserName    string `json:"user_name,omitempty"`
	WorkspaceID string `json:"workspace_id"`
	Role        string `json:"role"`
	TokenID     string `json:"token_id,omitempty"`
}

func (s *Server) publicAPI(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/accept-invite"
}

func (s *Server) authenticate(r *http.Request) (authPrincipal, bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	token, ok := strings.CutPrefix(raw, "Bearer ")
	if !ok || token == "" {
		return authPrincipal{}, false
	}
	if s.cfg.Server.AdminToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Server.AdminToken)) == 1 {
		return authPrincipal{Root: true, WorkspaceID: model.DefaultWorkspaceID, Role: model.RoleOwner}, true
	}
	principal, err := s.db.TokenPrincipalByHash(r.Context(), hashSecret(token))
	if err != nil {
		return authPrincipal{}, false
	}
	_ = s.db.TouchAPIToken(r.Context(), principal.Token.ID)
	return authPrincipal{
		UserID:      principal.User.ID,
		UserName:    principal.User.Name,
		WorkspaceID: principal.Token.WorkspaceID,
		Role:        principal.Role,
		TokenID:     principal.Token.ID,
	}, true
}

func (s *Server) authorizeAPI(r *http.Request, principal authPrincipal) bool {
	if principal.Root {
		return true
	}
	apiPath := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if apiPath == "/auth/me" {
		return r.Method == http.MethodGet
	}
	if apiPath == "/auth/invites" || apiPath == "/users" || strings.HasPrefix(apiPath, "/users/") {
		return principal.Role == model.RoleOwner
	}
	if strings.HasPrefix(apiPath, "/tokens/") {
		return r.Method == http.MethodDelete
	}
	if s.rootOnlyAPI(apiPath) {
		return false
	}
	if strings.Contains(apiPath, "/edge-manifest") && principal.Role == model.RoleViewer {
		return false
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	return principal.Role == model.RoleOwner || principal.Role == model.RoleMaintainer
}

func (s *Server) rootOnlyAPI(apiPath string) bool {
	return strings.HasPrefix(apiPath, "/init/") ||
		strings.HasPrefix(apiPath, "/resource-libraries/") ||
		strings.HasPrefix(apiPath, "/cloudflare/") ||
		strings.HasPrefix(apiPath, "/ipfs/") ||
		strings.HasPrefix(apiPath, "/jobs/") ||
		strings.HasPrefix(apiPath, "/objects/") ||
		apiPath == "/gc" ||
		apiPath == "/cache/purge" ||
		strings.Contains(apiPath, "/dns") ||
		strings.Contains(apiPath, "/worker-routes")
}

func currentPrincipal(ctx context.Context) authPrincipal {
	if principal, ok := ctx.Value(authContextKey{}).(authPrincipal); ok {
		return principal
	}
	return authPrincipal{Root: true, WorkspaceID: model.DefaultWorkspaceID, Role: model.RoleOwner}
}

func principalCanAccessWorkspace(principal authPrincipal, workspaceID string) bool {
	if principal.Root {
		return true
	}
	return workspaceID != "" && workspaceID == principal.WorkspaceID
}

func hashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func newSecret(prefix string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func newTokenID(prefix string) (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func validRole(role string) bool {
	switch role {
	case model.RoleOwner, model.RoleMaintainer, model.RoleViewer:
		return true
	default:
		return false
	}
}

func workspaceForContext(ctx context.Context) string {
	workspaceID := currentPrincipal(ctx).WorkspaceID
	if workspaceID == "" {
		return model.DefaultWorkspaceID
	}
	return workspaceID
}
