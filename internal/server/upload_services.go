package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type stagedFile struct {
	Path        string
	Size        int64
	SHA256      string
	ContentType string
}

func (s *Server) stageUpload(src io.Reader, name string) (*stagedFile, error) {
	tmp, err := os.CreateTemp(s.staging, "upload-*")
	if err != nil {
		return nil, err
	}
	defer tmp.Close()
	hash := sha256.New()
	var first bytes.Buffer
	writer := io.MultiWriter(tmp, hash)
	tee := io.TeeReader(io.LimitReader(src, 512), &first)
	n1, err := io.Copy(writer, tee)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	n2, err := io.Copy(writer, src)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	ctype := http.DetectContentType(first.Bytes())
	if strings.HasPrefix(ctype, "text/plain") {
		if byExt := mimeByName(name); byExt != "" {
			ctype = byExt
		}
	}
	return &stagedFile{
		Path:        tmp.Name(),
		Size:        n1 + n2,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		ContentType: ctype,
	}, nil
}

type putObjectInput struct {
	ProjectID      string
	ObjectPath     string
	Key            string
	Profile        config.RouteProfile
	ProfileName    string
	CacheControl   string
	ContentType    string
	Group          string
	FilePath       string
	FileName       string
	Size           int64
	SHA256         string
	BatchFileCount int
}

func (s *Server) putObjectFromFile(ctx context.Context, in putObjectInput) (*model.Object, []model.Job, error) {
	primary, ok := s.stores.Get(in.Profile.Primary)
	if !ok {
		return nil, nil, fmt.Errorf("primary storage %q is not configured", in.Profile.Primary)
	}
	var locator string
	err := s.withTransferSlot(ctx, func() error {
		var putErr error
		locator, putErr = primary.Put(ctx, storage.PutOptions{
			Key:            in.Key,
			FilePath:       in.FilePath,
			ContentType:    in.ContentType,
			CacheControl:   in.CacheControl,
			Group:          in.Group,
			SHA256:         in.SHA256,
			Size:           in.Size,
			FileName:       in.FileName,
			BatchFileCount: in.BatchFileCount,
			IgnoreLimits:   s.overclockMode(),
		})
		return putErr
	})
	if err != nil {
		return nil, nil, fmt.Errorf("put primary %s: %w", primary.Name(), err)
	}
	obj, err := s.db.SaveObject(ctx, model.Object{
		ProjectID:     in.ProjectID,
		Path:          in.ObjectPath,
		Key:           in.Key,
		RouteProfile:  in.ProfileName,
		Size:          in.Size,
		SHA256:        in.SHA256,
		ContentType:   in.ContentType,
		CacheControl:  in.CacheControl,
		PrimaryTarget: in.Profile.Primary,
	})
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.db.UpsertReplica(ctx, obj.ID, in.Profile.Primary, model.ReplicaReady, locator, ""); err != nil {
		return nil, nil, err
	}
	if err := s.recordIPFSReplica(ctx, obj.ID, in.Profile.Primary, primary, locator); err != nil {
		return nil, nil, err
	}
	var jobs []model.Job
	policy := replicationPolicyForProfile(in.Profile)
	for _, target := range routeProfileBackupTargets(in.Profile) {
		if _, ok := s.stores.Get(target); !ok {
			return nil, nil, fmt.Errorf("backup storage %q is not configured", target)
		}
		switch policy {
		case config.ReplicationPolicyPrimaryOnly:
			if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaDeleted, "", "replication_policy primary_only"); err != nil {
				return nil, nil, err
			}
			if err := s.db.DeleteIPFSPin(ctx, obj.ID, target); err != nil {
				return nil, nil, err
			}
		case config.ReplicationPolicyRequireBackups:
			if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaPending, "", ""); err != nil {
				return nil, nil, err
			}
			if err := s.db.DeleteIPFSPin(ctx, obj.ID, target); err != nil {
				return nil, nil, err
			}
			if err := s.replicateObject(ctx, replicatePayload{ObjectID: obj.ID, Target: target}); err != nil {
				return nil, nil, fmt.Errorf("replicate required backup %q: %w", target, err)
			}
		default:
			if _, err := s.db.UpsertReplica(ctx, obj.ID, target, model.ReplicaPending, "", ""); err != nil {
				return nil, nil, err
			}
			if err := s.db.DeleteIPFSPin(ctx, obj.ID, target); err != nil {
				return nil, nil, err
			}
			payload, _ := json.Marshal(replicatePayload{ObjectID: obj.ID, Target: target})
			job, err := s.db.CreateJob(ctx, model.JobReplicateObject, string(payload))
			if err != nil {
				return nil, nil, err
			}
			jobs = append(jobs, *job)
		}
	}
	obj, err = s.hydrateObjectIPFS(ctx, obj)
	if err != nil {
		return nil, nil, err
	}
	return obj, jobs, nil
}
