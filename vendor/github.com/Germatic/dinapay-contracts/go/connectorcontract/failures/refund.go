package failures

// RefundCode is intentionally separate from payment and payout codes. A code
// may share its string value with another catalog, but its validity is scoped
// to refund resources.
type RefundCode string

const (
	RefundComplianceRejected  RefundCode = "compliance_rejected"
	RefundLimitExceeded       RefundCode = "limit_exceeded"
	RefundProviderUnavailable RefundCode = "provider_unavailable"
	RefundProcessingError     RefundCode = "processing_error"
	RefundUnknownError        RefundCode = "unknown_error"
	RefundInsufficientFunds   RefundCode = "insufficient_funds"
	RefundRejected            RefundCode = "refund_rejected"
)

var refunds = catalog(map[string]definition{
	"insufficient_funds": {"funding", "No hay fondos suficientes para procesar el reembolso."},
	"refund_rejected":    {"refund", "El reembolso fue rechazado."},
})

func NewRefund(code RefundCode, fields ...FieldError) Failure {
	return build(string(code), refunds, fields...)
}

func ValidRefund(value Failure) bool { return valid(value, refunds) }
func IsRefundCode(code string) bool  { _, ok := refunds[code]; return ok }
