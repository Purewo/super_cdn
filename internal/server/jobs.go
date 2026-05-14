package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"supercdn/internal/model"
)

func (s *Server) jobLoop(ctx context.Context, workerID int) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for {
				job, err := s.db.NextQueuedJob(ctx)
				if errors.Is(err, sql.ErrNoRows) {
					break
				}
				if err != nil {
					s.logger.Warn("load job failed", "error", err)
					break
				}
				result, err := s.runJob(ctx, job)
				if err != nil {
					retry := job.Attempts < 5
					if job.Type == model.JobDeploySite {
						retry = false
					}
					if failErr := s.db.FailJobWithResult(ctx, job.ID, err.Error(), retry, result); failErr != nil {
						s.logger.Warn("mark job failed", "worker", workerID, "job", job.ID, "error", failErr)
					}
					s.logger.Warn("job failed", "worker", workerID, "job", job.ID, "retry", retry, "error", err)
					continue
				}
				if err := s.db.FinishJobWithResult(ctx, job.ID, result); err != nil {
					s.logger.Warn("mark job done failed", "worker", workerID, "job", job.ID, "error", err)
				}
			}
		}
	}
}

func (s *Server) runJob(ctx context.Context, job *model.Job) (string, error) {
	switch job.Type {
	case model.JobReplicateObject:
		var payload replicatePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return "", err
		}
		return "", s.replicateObject(ctx, payload)
	case model.JobInitResourceLibraries:
		var payload initResourceLibrariesPayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return "", err
		}
		result, err := s.initResourceLibraries(ctx, payload, false)
		raw, _ := json.Marshal(result)
		return string(raw), err
	case model.JobDeploySite:
		var payload deploySitePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return "", err
		}
		result, err := s.processSiteDeployment(ctx, payload)
		raw, _ := json.Marshal(result)
		return string(raw), err
	default:
		return "", fmt.Errorf("unknown job type %q", job.Type)
	}
}
