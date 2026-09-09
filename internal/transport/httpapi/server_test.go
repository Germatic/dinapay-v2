package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDIsGeneratedAndReturnedInErrors(t *testing.T) {
	h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusBadRequest, "invalid_request", "bad request")
	}))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	id := recorder.Header().Get("X-Request-Id")
	if id == "" {
		t.Fatal("missing X-Request-Id response header")
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"requestId":"`+id+`"`) {
		t.Fatalf("response body does not contain request id %q: %s", id, body)
	}
}

func TestValidClientRequestIDIsPreserved(t *testing.T) {
	h := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "merchant-request-123")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if got := recorder.Header().Get("X-Request-Id"); got != "merchant-request-123" {
		t.Fatalf("X-Request-Id = %q", got)
	}
}
