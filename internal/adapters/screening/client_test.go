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

func TestCanonicalRiskRequest(t *testing.T) {
	var path, body string
	c := New("http://screening", "key", "sandbox", time.Second, true, nil)
	c.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"result":"minor","action":"continue","policy":{"mode":"observe","provider":"arca"}}`)), Header: http.Header{}}, nil
	})}
	err := c.EvaluateRisk(context.Background(), core.RiskControlObservation{OperationID: "risk:payment:p1:customer:age_check", ControlType: "age_check", Stage: "creation", SubjectRole: "customer", AccountID: "a", MerchantID: "m", ResourceType: "payment", ResourceID: "p1", Country: "AR", Subject: map[string]any{"type": "individual", "documentNumber": "20123456717", "country": "AR"}})
	if err != nil || path != "/internal/v1/risk-evaluations" || !strings.Contains(body, `"controlType":"age_check"`) || !strings.Contains(body, `"subjectRole":"customer"`) || !strings.Contains(body, `"type":"individual"`) {
		t.Fatalf("err=%v path=%s body=%s", err, path, body)
	}
}
