package failures

type PayoutCode string

const (
	PayoutComplianceRejected     PayoutCode = "compliance_rejected"
	PayoutLimitExceeded          PayoutCode = "limit_exceeded"
	PayoutProviderUnavailable    PayoutCode = "provider_unavailable"
	PayoutProcessingError        PayoutCode = "processing_error"
	PayoutUnknownError           PayoutCode = "unknown_error"
	PayoutInvalidRemitterData    PayoutCode = "invalid_remitter_data"
	PayoutInvalidBeneficiaryData PayoutCode = "invalid_beneficiary_data"
	PayoutInvalidAmount          PayoutCode = "invalid_amount"
	PayoutInvalidDestination     PayoutCode = "invalid_destination"
	PayoutDestinationRejected    PayoutCode = "destination_rejected"
	PayoutBeneficiaryBlocked     PayoutCode = "beneficiary_blocked"
	PayoutInsufficientFunds      PayoutCode = "insufficient_funds"
	PayoutCancelled              PayoutCode = "payout_cancelled"
)

var payouts = catalog(map[string]definition{
	"invalid_remitter_data":    {"validation", "Los datos del remitente no son válidos."},
	"invalid_beneficiary_data": {"validation", "Los datos del beneficiario no son válidos."},
	"invalid_amount":           {"validation", "El monto indicado no es válido para esta operación."},
	"invalid_destination":      {"destination", "El destino indicado no es válido."},
	"destination_rejected":     {"destination", "La entidad receptora rechazó la operación."},
	"beneficiary_blocked":      {"compliance", "El beneficiario no puede recibir la operación."},
	"insufficient_funds":       {"funding", "No hay fondos suficientes para procesar la operación."},
	"payout_cancelled":         {"lifecycle", "El envío fue cancelado."},
})

func NewPayout(code PayoutCode, fields ...FieldError) Failure {
	return build(string(code), payouts, fields...)
}
func ValidPayout(value Failure) bool { return valid(value, payouts) }
func IsPayoutCode(code string) bool  { _, ok := payouts[code]; return ok }
