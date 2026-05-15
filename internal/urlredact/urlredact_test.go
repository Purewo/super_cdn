package urlredact

import (
	"strings"
	"testing"
)

func TestRedactSignedQueryValues(t *testing.T) {
	got := RedactSignedQueryValues("https://storage.example/app.js?X-Amz-Date=20260430T000000Z&X-Amz-Signature=secret&plain=keep")
	for _, leaked := range []string{"20260430T000000Z", "secret", "plain=keep"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted URL leaked %q: %s", leaked, got)
		}
	}
	for _, want := range []string{"X-Amz-Date=%3Credacted%3E", "X-Amz-Signature=%3Credacted%3E", "plain=%3Credacted%3E"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted URL missing %q: %s", want, got)
		}
	}
}

func TestRedactSignedQueryValuesLeavesPlainQueries(t *testing.T) {
	raw := "https://storage.example/app.js?plain=keep"
	if got := RedactSignedQueryValues(raw); got != raw {
		t.Fatalf("plain query redacted: %s", got)
	}
}

func TestRedactAllQueryValues(t *testing.T) {
	got, ok := RedactAllQueryValues("https://storage.example/app.js?plain=keep&token=secret")
	if !ok {
		t.Fatal("expected redaction")
	}
	if strings.Contains(got, "keep") || strings.Contains(got, "secret") {
		t.Fatalf("query values were not redacted: %s", got)
	}
}

func TestLooksSigned(t *testing.T) {
	if !LooksSigned("https://storage.example/app.js?sign=abc") {
		t.Fatal("expected signed URL")
	}
	if LooksSigned("https://storage.example/app.js?plain=keep") {
		t.Fatal("plain query should not look signed")
	}
}
