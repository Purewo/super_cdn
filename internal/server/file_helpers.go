package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"os"

	"supercdn/internal/storage"
)

func siteDeploymentRootKey(siteID, deploymentID, filePath string) string {
	return storage.JoinKey("sites", siteID, "deployments", deploymentID, "root", filePath)
}

func statLocalFile(filePath, name string) (*stagedFile, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	hash := sha256.New()
	var first bytes.Buffer
	tee := io.TeeReader(io.LimitReader(f, 512), &first)
	n1, err := io.Copy(hash, tee)
	if err != nil {
		return nil, err
	}
	n2, err := io.Copy(hash, f)
	if err != nil {
		return nil, err
	}
	ctype := http.DetectContentType(first.Bytes())
	if byExt := mimeByName(name); byExt != "" {
		ctype = byExt
	}
	return &stagedFile{
		Path:        filePath,
		Size:        n1 + n2,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
		ContentType: ctype,
	}, nil
}

func writeTempPayload(dir, pattern string, payload []byte, name string) (string, *stagedFile, error) {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(payload)
	if err := closeErr(tmp, err); err != nil {
		_ = os.Remove(tmpPath)
		return "", nil, err
	}
	stat, err := statLocalFile(tmpPath, name)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", nil, err
	}
	return tmpPath, stat, nil
}

func firstFormFile(r *http.Request, names ...string) (multipart.File, *multipart.FileHeader, error) {
	for _, name := range names {
		f, h, err := r.FormFile(name)
		if err == nil {
			return f, h, nil
		}
	}
	return nil, nil, http.ErrMissingFile
}
