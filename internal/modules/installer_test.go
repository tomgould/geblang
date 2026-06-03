package modules

import (
	"strings"
	"testing"
)

func TestPickLatestSemverTagPicksHighestStable(t *testing.T) {
	out := strings.Join([]string{
		"abc\trefs/tags/v1.0.0",
		"abc\trefs/tags/v1.0.1",
		"abc\trefs/tags/v1.0.2",
		"abc\trefs/tags/v1.1.0",
	}, "\n")
	got, err := pickLatestSemverTag(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v1.1.0" {
		t.Fatalf("got %q want v1.1.0", got)
	}
}

func TestPickLatestSemverTagPrefersStableOverPrerelease(t *testing.T) {
	out := strings.Join([]string{
		"abc\trefs/tags/v1.1.0",
		"abc\trefs/tags/v1.2.0-rc1",
	}, "\n")
	got, err := pickLatestSemverTag(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v1.1.0" {
		t.Fatalf("got %q want v1.1.0 (stable wins over rc)", got)
	}
}

func TestPickLatestSemverTagAllPrereleasePicksHighest(t *testing.T) {
	out := strings.Join([]string{
		"abc\trefs/tags/v1.0.0-rc1",
		"abc\trefs/tags/v1.0.0-rc2",
	}, "\n")
	got, err := pickLatestSemverTag(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v1.0.0-rc2" {
		t.Fatalf("got %q want v1.0.0-rc2", got)
	}
}

func TestPickLatestSemverTagAcceptsBareNumeric(t *testing.T) {
	out := strings.Join([]string{
		"abc\trefs/tags/1.0.0",
		"abc\trefs/tags/1.1.0",
	}, "\n")
	got, err := pickLatestSemverTag(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "1.1.0" {
		t.Fatalf("got %q want 1.1.0", got)
	}
}

func TestPickLatestSemverTagSkipsNonSemverTags(t *testing.T) {
	out := strings.Join([]string{
		"abc\trefs/tags/release-1",
		"abc\trefs/tags/v1.0.0",
		"abc\trefs/tags/dev",
	}, "\n")
	got, err := pickLatestSemverTag(out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v1.0.0" {
		t.Fatalf("got %q want v1.0.0", got)
	}
}

func TestPickLatestSemverTagErrorsWhenNoneFound(t *testing.T) {
	out := strings.Join([]string{
		"abc\trefs/tags/release-1",
		"abc\trefs/tags/dev",
	}, "\n")
	if _, err := pickLatestSemverTag(out); err == nil {
		t.Fatal("expected error for no-semver-tags input")
	}
}
