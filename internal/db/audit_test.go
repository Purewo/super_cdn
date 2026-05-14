package db

import (
	"context"
	"path/filepath"
	"testing"

	"supercdn/internal/model"
)

func TestCreateAndListAuditEvents(t *testing.T) {
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "audit.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer state.Close()

	event, err := state.CreateAuditEvent(ctx, model.AuditEvent{
		WorkspaceID: "workspace-1",
		UserID:      42,
		Action:      "site.create",
		Resource:    "site:demo",
	})
	if err != nil {
		t.Fatalf("CreateAuditEvent() error = %v", err)
	}
	if event.ID == 0 || event.CreatedAt.IsZero() {
		t.Fatalf("unexpected audit event: %+v", event)
	}

	events, err := state.AuditEvents(ctx, "workspace-1", 10)
	if err != nil {
		t.Fatalf("AuditEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("AuditEvents() len = %d, want 1", len(events))
	}
	if events[0].Action != "site.create" || events[0].Resource != "site:demo" || events[0].UserID != 42 {
		t.Fatalf("unexpected listed audit event: %+v", events[0])
	}

	if _, err := state.CreateAuditEvent(ctx, model.AuditEvent{
		WorkspaceID: "workspace-1",
		Action:      "asset_bucket.object.upload",
		Resource:    "asset_bucket:docs;path:guides/readme.txt",
	}); err != nil {
		t.Fatalf("CreateAuditEvent() second error = %v", err)
	}
	filtered, err := state.AuditEventsFiltered(ctx, AuditEventFilter{
		WorkspaceID:      "workspace-1",
		Action:           "asset_bucket.object.upload",
		ResourceContains: "guides/readme",
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("AuditEventsFiltered() error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].Action != "asset_bucket.object.upload" {
		t.Fatalf("unexpected filtered events: %+v", filtered)
	}
}
