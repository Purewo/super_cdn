package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/model"
	"supercdn/internal/storage"
)

type lineHealthTestStore struct {
	storage.Store
	name    string
	started chan<- string
	release <-chan struct{}
	err     error
}

func (s *lineHealthTestStore) Name() string { return s.name }
func (s *lineHealthTestStore) Type() string { return "resource_library" }
func (s *lineHealthTestStore) HealthCheck(ctx context.Context, _ storage.HealthCheckOptions) (*storage.HealthCheckResult, error) {
	if s.started != nil {
		s.started <- s.name
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	item := storage.HealthCheckItem{BindingName: s.name + "_binding", Status: storage.HealthStatusOK}
	if s.err != nil {
		item.Status = storage.HealthStatusFailed
		item.LastError = s.err.Error()
	}
	return &storage.HealthCheckResult{Items: []storage.HealthCheckItem{item}}, s.err
}

func TestLineHealthChecksLibrariesConcurrently(t *testing.T) {
	app := newTestServer(t)
	app.cfg.ResourceLibraries = []config.ResourceLibraryConfig{{Name: "domestic"}, {Name: "overseas"}}
	started := make(chan string, 2)
	release := make(chan struct{})
	app.stores = storage.NewManager([]storage.Store{
		&lineHealthTestStore{name: "domestic", started: started, release: release},
		&lineHealthTestStore{name: "overseas", started: started, release: release},
	})
	done := make(chan lineHealthResponse, 1)
	go func() { done <- app.lineHealth(context.Background(), time.Second) }()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			close(release)
			t.Fatal("line checks did not start concurrently")
		}
	}
	close(release)
	resp := <-done
	if resp.Status != "ok" || len(resp.Lines) != 2 || resp.Lines[0].Status != "ok" || resp.Lines[1].Status != "ok" {
		t.Fatalf("line health = %+v", resp)
	}
}

func TestLineHealthReturnsDeadlineWithoutWaitingForSlowProvider(t *testing.T) {
	app := newTestServer(t)
	app.cfg.ResourceLibraries = []config.ResourceLibraryConfig{{Name: "slow"}}
	app.stores = storage.NewManager([]storage.Store{
		&lineHealthTestStore{name: "slow", release: make(chan struct{})},
	})
	started := time.Now()
	resp := app.lineHealth(context.Background(), 25*time.Millisecond)
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("line health exceeded its deadline")
	}
	if resp.Status != "degraded" || len(resp.Lines) != 1 || resp.Lines[0].Status != "timeout" {
		t.Fatalf("deadline response = %+v", resp)
	}
}

func TestLineHealthRequiresTokenAndRedactsProviderError(t *testing.T) {
	app := newTestServer(t)
	app.cfg.ResourceLibraries = []config.ResourceLibraryConfig{{Name: "domestic"}}
	app.stores = storage.NewManager([]storage.Store{
		&lineHealthTestStore{name: "domestic", err: errors.New("https://private.example/path?token=secret: code=401")},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/line-health", nil)
	unauthorized := httptest.NewRecorder()
	app.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	if app.authorizeAPI(request, authPrincipal{Role: model.RoleViewer}) {
		t.Fatal("viewer should not run provider probes")
	}
	if !app.authorizeAPI(request, authPrincipal{Role: model.RoleOwner}) {
		t.Fatal("owner should be allowed to run provider probes")
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/line-health", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "private.example") {
		t.Fatalf("unsafe line-health response: status=%d body=%s", response.Code, response.Body.String())
	}
	var report lineHealthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "degraded" || len(report.Lines) != 1 || report.Lines[0].Reason != "unauthorized" {
		t.Fatalf("line-health report = %+v", report)
	}
	if report.DeadlineMS != lineHealthBudget.Milliseconds() {
		t.Fatalf("deadline_ms = %d", report.DeadlineMS)
	}
}
