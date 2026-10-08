package screening

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestObserveContinuesAndNormalizesCustomer(t *testing.T) {
	var body string
	c := New("http://screening", "key", "sandbox", time.Second, true, nil)
	c.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"mode":"observe","decision":"review","action":"continue","providerEvidence":{"provider":"mock"}}`)), Header: http.Header{}}, nil
	})}
	err := c.Evaluate(context.Background(), core.ScreeningObservation{OperationID: "op", AccountID: "a", MerchantID: "m", ResourceType: "payment", ResourceID: "p", Country: "AR", Environment: "sandbox", Subject: map[string]any{"name": "Juan Perez", "country": "AR"}})
	if err != nil || !strings.Contains(body, `"firstName":"Juan"`) || !strings.Contains(body, `"lastName":"Perez"`) {
		t.Fatalf("err=%v body=%s", err, body)
	}
}
func TestFailOpenOnUnavailable(t *testing.T) {
	c := New("http://screening", "", "sandbox", time.Millisecond, true, nil)
	c.http = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })}
	if err := c.Evaluate(context.Background(), core.ScreeningObservation{ResourceType: "payment"}); err != nil {
		t.Fatal(err)
	}
}
