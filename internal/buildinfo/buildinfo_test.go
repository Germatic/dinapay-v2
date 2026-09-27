package buildinfo

import "testing"

func TestCurrentHasCanonicalIdentity(t *testing.T) {
	t.Setenv("DINARIA_ENVIRONMENT", "sandbox")
	got := Current()
	if got.Service != "dinapay-v2" || got.Repository != "github.com/Germatic/dinapay-v2" || got.ContractVersion != "v2" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.Environment != "sandbox" {
		t.Fatalf("environment = %q", got.Environment)
	}
}
