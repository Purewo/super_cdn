package main

import (
	"reflect"
	"testing"
)

func TestAppendCommandHintsTrimsAndDeduplicates(t *testing.T) {
	got := appendCommandHints(
		[]string{" supercdnctl doctor ", "supercdnctl probe-site"},
		"",
		"supercdnctl doctor",
		" supercdnctl cdn-doctor -bucket assets ",
	)
	want := []string{
		"supercdnctl doctor",
		"supercdnctl probe-site",
		"supercdnctl cdn-doctor -bucket assets",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appendCommandHints = %#v, want %#v", got, want)
	}
}

func TestCLIHintArgQuotesPowerShellRiskyValues(t *testing.T) {
	cases := map[string]string{
		"":              "''",
		"assets/app.js": "assets/app.js",
		"dist old":      "'dist old'",
		"Bob's site":    "'Bob''s site'",
		"$env:TOKEN":    "'$env:TOKEN'",
	}
	for in, want := range cases {
		if got := cliHintArg(in); got != want {
			t.Fatalf("cliHintArg(%q) = %q, want %q", in, got, want)
		}
	}
}
