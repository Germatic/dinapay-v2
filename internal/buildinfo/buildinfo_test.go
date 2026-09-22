package buildinfo

import (
	"strings"
	"testing"
)

func TestCurrentReturnsCanonicalMetadata(t *testing.T) {
	t.Setenv("DINARIA_ENVIRONMENT", "sandbox")
	oldVersion, oldCommit, oldBuiltAt := Version, Commit, BuiltAt
	Version, Commit, BuiltAt = "2.3.4", "abc123", "2026-09-22T00:00:00Z"
	t.Cleanup(func() { Version, Commit, BuiltAt = oldVersion, oldCommit, oldBuiltAt })

	got := Current("service-under-test")
	if got.Service != "service-under-test" || got.Version != "2.3.4" || got.Commit != "abc123" || got.BuiltAt != "2026-09-22T00:00:00Z" || got.Environment != "sandbox" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if !strings.HasPrefix(got.GoVersion, "go") {
		t.Fatalf("unexpected Go version: %q", got.GoVersion)
	}
}
