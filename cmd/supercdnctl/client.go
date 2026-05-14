package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type client struct {
	baseURL string
	token   string
	http    *http.Client
}

func (c client) doJSON(method, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.do(method, path, bytes.NewReader(raw), "application/json")
}

func (c client) doJSONQuiet(method, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func (c client) doJSONRaw(method, path string, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.doRaw(method, path, bytes.NewReader(raw), "application/json")
}

func (c client) uploadFile(path, fieldName, filePath string, fields map[string]string) error {
	raw, err := c.uploadFileRaw(path, fieldName, filePath, fields)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func (c client) uploadFileRaw(path, fieldName, filePath string, fields map[string]string) ([]byte, error) {
	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	contentType := multipartWriter.FormDataContentType()
	go func() {
		var err error
		defer func() {
			if err != nil {
				_ = writer.CloseWithError(err)
				return
			}
			_ = writer.Close()
		}()
		for k, v := range fields {
			if v != "" {
				if err = multipartWriter.WriteField(k, v); err != nil {
					return
				}
			}
		}
		f, openErr := os.Open(filePath)
		if openErr != nil {
			err = openErr
			return
		}
		defer f.Close()
		part, createErr := multipartWriter.CreateFormFile(fieldName, filepath.Base(filePath))
		if createErr != nil {
			err = createErr
			return
		}
		if _, copyErr := io.Copy(part, f); copyErr != nil {
			err = copyErr
			return
		}
		err = multipartWriter.Close()
	}()
	return c.doRaw(http.MethodPost, path, reader, contentType)
}

func (c client) do(method, path string, body io.Reader, contentType string) error {
	raw, err := c.doRaw(method, path, body, contentType)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func (c client) doRaw(method, path string, body io.Reader, contentType string) ([]byte, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

func (c client) waitDeployment(site, deployment string, timeout time.Duration) error {
	raw, err := c.waitDeploymentRaw(site, deployment, timeout)
	if err != nil {
		_ = printJSON(raw)
		return err
	}
	return printJSON(raw)
}

func (c client) waitDeploymentRaw(site, deployment string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		raw, err := c.doRaw(http.MethodGet, "/api/v1/sites/"+url.PathEscape(site)+"/deployments/"+url.PathEscape(deployment), nil, "")
		if err != nil {
			return nil, err
		}
		var dep struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &dep); err != nil {
			return raw, err
		}
		switch dep.Status {
		case "ready", "active":
			return raw, nil
		case "failed":
			return raw, errors.New("deployment failed")
		}
		if time.Now().After(deadline) {
			return raw, errors.New("deployment wait timed out")
		}
		time.Sleep(2 * time.Second)
	}
}
