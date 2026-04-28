package storage

import "testing"

func TestCleanObjectPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "normal", in: "/img/logo.png", want: "img/logo.png", ok: true},
		{name: "windows separators", in: `css\app.css`, want: "css/app.css", ok: true},
		{name: "empty", in: "/", ok: false},
		{name: "traversal", in: "../secret.txt", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CleanObjectPath(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("expected error, got %q", got)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestCleanDirectoryPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "normal", in: "/assets/objects", want: "assets/objects", ok: true},
		{name: "windows separators", in: `sites\releases`, want: "sites/releases", ok: true},
		{name: "root", in: "/", want: "", ok: true},
		{name: "traversal", in: "../secret", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CleanDirectoryPath(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("expected error, got %q", got)
			}
			if got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}
