package server

import (
	"net/http"
	"strconv"
	"strings"

	"supercdn/internal/db"
	"supercdn/internal/model"
)

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := s.db.GetJob(r.Context(), id)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleGetInitJob(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := s.db.GetJob(r.Context(), id)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job.Type != model.JobInitResourceLibraries {
		writeError(w, http.StatusNotFound, "init job not found")
		return
	}
	writeJSON(w, http.StatusOK, jobView(job))
}

func (s *Server) handleObjectReplicas(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid object id")
		return
	}
	replicas, err := s.hydrateReplicasIPFS(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, replicas)
}

func (s *Server) handleRefreshObjectReplicas(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid object id")
		return
	}
	var req refreshObjectReplicasRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	obj, err := s.db.GetObject(r.Context(), id)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "object not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result, err := s.refreshObjectReplicas(r.Context(), obj, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRepairObjectReplicas(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid object id")
		return
	}
	var req repairObjectReplicasRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	obj, err := s.db.GetObject(r.Context(), id)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "object not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result, err := s.repairObjectReplicas(r.Context(), obj, req)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "storage target") || strings.Contains(err.Error(), "not configured") {
			status = http.StatusBadGateway
		}
		writeError(w, status, err.Error())
		return
	}
	if len(result.Errors) > 0 {
		result.Status = "partial"
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePurgeCache(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs              []string `json:"urls"`
		CloudflareAccount string   `json:"cloudflare_account"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	account, ok := s.cfg.CloudflareAccountByName(req.CloudflareAccount)
	if !ok {
		writeError(w, http.StatusBadRequest, "cloudflare account not found")
		return
	}
	cf := s.cloudflareClientForAccount(account)
	if !cf.Configured() {
		writeJSON(w, http.StatusOK, map[string]any{"status": "skipped", "reason": "cloudflare zone_id/api_token not configured"})
		return
	}
	raw, err := cf.PurgeCache(r.Context(), req.URLs)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}
