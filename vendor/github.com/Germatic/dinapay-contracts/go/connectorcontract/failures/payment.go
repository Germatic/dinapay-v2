package failures

type PaymentCode string

const (
	PaymentComplianceRejected  PaymentCode = "compliance_rejected"
	PaymentLimitExceeded       PaymentCode = "limit_exceeded"
	PaymentProviderUnavailable PaymentCode = "provider_unavailable"
	PaymentProcessingError     PaymentCode = "processing_error"
	PaymentUnknownError        PaymentCode = "unknown_error"
	PaymentInvalidCustomerData PaymentCode = "invalid_customer_data"
	PaymentMethodUnavailable   PaymentCode = "payment_method_unavailable"
	PaymentExpired             PaymentCode = "payment_expired"
	PaymentPayerCancelled      PaymentCode = "payer_cancelled"
	PaymentAmountMismatch      PaymentCode = "amount_mismatch"
	PaymentRejected            PaymentCode = "payment_rejected"
	PaymentNotReceived         PaymentCode = "payment_not_received"
)

var payments = catalog(map[string]definition{
	"invalid_customer_data":      {"validation", "Los datos del pagador no son válidos."},
	"payment_method_unavailable": {"payment_method", "El medio de pago no está disponible."},
	"payment_expired":            {"lifecycle", "El plazo para realizar el pago venció."},
	"payer_cancelled":            {"customer", "El pagador canceló la operación."},
	"amount_mismatch":            {"reconciliation", "El importe recibido no coincide con el importe esperado."},
	"payment_rejected":           {"payment", "El pago fue rechazado."},
	"payment_not_received":       {"reconciliation", "No se recibió el pago."},
})

func NewPayment(code PaymentCode, fields ...FieldError) Failure {
	return build(string(code), payments, fields...)
}
func ValidPayment(value Failure) bool { return valid(value, payments) }
func IsPaymentCode(code string) bool  { _, ok := payments[code]; return ok }
