package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Germatic/dinapay-v2/internal/core"
)

type riskSubject struct {
	role    string
	subject map[string]any
}

func evaluateCreationRisk(ctx context.Context, gate core.RiskControlGate, base core.RiskControlObservation, subjects ...riskSubject) error {
	if gate == nil {
		return nil
	}
	type call struct{ observation core.RiskControlObservation }
	calls := make([]call, 0, len(subjects)*2)
	for _, party := range subjects {
		if len(party.subject) == 0 {
			continue
		}
		country := base.Country
		if ownCountry, ok := party.subject["country"].(string); ok && strings.TrimSpace(ownCountry) != "" {
			country = strings.TrimSpace(ownCountry)
		}
		for _, control := range []string{"screening", "age_check"} {
			if control == "age_check" && !hasSubjectDocument(party.subject) {
				continue
			}
			observation := base
			observation.ControlType = control
			observation.Stage = "creation"
			observation.SubjectRole = party.role
			observation.Subject = party.subject
			observation.Country = country
			observation.OperationID = fmt.Sprintf("risk:%s:%s:creation:%s:%s", base.ResourceType, base.ResourceID, party.role, control)
			calls = append(calls, call{observation: observation})
		}
	}
	errs := make([]error, len(calls))
	var wait sync.WaitGroup
	for i := range calls {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errs[index] = gate.EvaluateRisk(ctx, calls[index].observation)
		}(i)
	}
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func hasSubjectDocument(subject map[string]any) bool {
	value, ok := subject["documentNumber"].(string)
	return ok && strings.TrimSpace(value) != ""
}
