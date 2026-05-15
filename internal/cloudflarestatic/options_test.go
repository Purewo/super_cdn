package cloudflarestatic

import "testing"

func TestNormalizeCachePolicy(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", CachePolicyAuto},
		{" AUTO ", CachePolicyAuto},
		{"force", CachePolicyForce},
		{"none", CachePolicyNone},
	}
	for _, tc := range cases {
		got, err := NormalizeCachePolicy(tc.in)
		if err != nil {
			t.Fatalf("NormalizeCachePolicy(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("NormalizeCachePolicy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := NormalizeCachePolicy("off"); err == nil {
		t.Fatal("expected invalid cache policy error")
	}
}

func TestNormalizeNotFoundHandling(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"none", ""},
		{" 404-PAGE ", NotFoundHandling404},
		{"single-page-application", NotFoundHandlingSPA},
	}
	for _, tc := range cases {
		got, err := NormalizeNotFoundHandling(tc.in)
		if err != nil {
			t.Fatalf("NormalizeNotFoundHandling(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("NormalizeNotFoundHandling(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := NotFoundHandlingFlag("none", true); got != NotFoundHandlingSPA {
		t.Fatalf("NotFoundHandlingFlag spa = %q, want %q", got, NotFoundHandlingSPA)
	}
	if _, err := NormalizeNotFoundHandling("rewrite"); err == nil {
		t.Fatal("expected invalid not-found handling error")
	}
}

func TestNormalizeVerifyMode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", VerifyModeWait},
		{" WAIT ", VerifyModeWait},
		{"warn", VerifyModeWarn},
		{"none", VerifyModeNone},
	}
	for _, tc := range cases {
		got, err := NormalizeVerifyMode(tc.in)
		if err != nil {
			t.Fatalf("NormalizeVerifyMode(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("NormalizeVerifyMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := NormalizeVerifyMode("off"); err == nil {
		t.Fatal("expected invalid verify mode error")
	}
}
