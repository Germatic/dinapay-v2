package core

func PublicStatusFromProvider(status string) (string, bool) {
	switch status {
	case "created":
		return "started", true
	case "pending":
		return "pending", true
	case "confirmed":
		return "confirmed", true
	case "rejected", "failed":
		return "failed", true
	case "cancelled":
		return "cancelled", true
	case "expired":
		return "expired", true
	default:
		return "", false
	}
}

func PaymentTransitionAllowed(current, next string) bool {
	if current == next {
		return true
	}
	switch current {
	case "started":
		return next == "pending" || next == "confirmed" || next == "failed" || next == "cancelled" || next == "expired"
	case "pending":
		return next == "confirmed" || next == "failed" || next == "cancelled" || next == "expired"
	default:
		return false
	}
}
