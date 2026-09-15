package webhooks

import (
	"strings"
	"testing"
)

func TestSignatureHeaderIncludesPreviousSecretDuringRotation(t *testing.T) {
	header := signatureHeader("current", "previous", []byte(`{"eventId":"1"}`), 123)
	if !strings.HasPrefix(header, "t=123,v1=") || strings.Count(header, "v1=") != 2 {
		t.Fatalf("header=%q", header)
	}
}

func TestSignatureHeaderWithoutPreviousSecret(t *testing.T) {
	header := signatureHeader("current", "", []byte(`{}`), 123)
	if strings.Count(header, "v1=") != 1 {
		t.Fatalf("header=%q", header)
	}
}
