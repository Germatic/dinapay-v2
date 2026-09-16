package failures

type Failure struct {
	Code     string       `json:"code"`
	Category string       `json:"category"`
	Message  string       `json:"message"`
	Errors   []FieldError `json:"errors,omitempty"`
}

type FieldError struct {
	Code    FieldCode `json:"code"`
	Field   string    `json:"field"`
	Message string    `json:"message"`
}

// ProviderFailure is internal evidence. It must never be copied into a
// merchant response or merchant webhook.
type ProviderFailure struct {
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

type FieldCode string

const (
	FieldRequired           FieldCode = "required"
	FieldInvalidFormat      FieldCode = "invalid_format"
	FieldInvalidType        FieldCode = "invalid_type"
	FieldInvalidValue       FieldCode = "invalid_value"
	FieldUnsupportedValue   FieldCode = "unsupported_value"
	FieldOutOfRange         FieldCode = "out_of_range"
	FieldInconsistentFields FieldCode = "inconsistent_fields"
)

type definition struct{ category, message string }

var common = map[string]definition{
	"compliance_rejected":  {"compliance", "La operación fue rechazada por controles de cumplimiento."},
	"limit_exceeded":       {"limits", "La operación excede los límites permitidos."},
	"provider_unavailable": {"processing", "No fue posible procesar la operación temporalmente."},
	"processing_error":     {"processing", "No fue posible completar el procesamiento de la operación."},
	"unknown_error":        {"unknown", "No fue posible procesar la operación."},
}

func catalog(specific map[string]definition) map[string]definition {
	out := make(map[string]definition, len(common)+len(specific))
	for code, value := range common {
		out[code] = value
	}
	for code, value := range specific {
		out[code] = value
	}
	return out
}

func build(code string, definitions map[string]definition, fields ...FieldError) Failure {
	value, ok := definitions[code]
	if !ok {
		code, value = "unknown_error", definitions["unknown_error"]
	}
	return Failure{Code: code, Category: value.category, Message: value.message, Errors: fields}
}

func valid(value Failure, definitions map[string]definition) bool {
	want, ok := definitions[value.Code]
	return ok && value.Category == want.category && value.Message == want.message
}
