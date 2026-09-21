package core

import (
	"errors"
	"testing"
)

func TestValidateMoneyUsesAssetPrecision(t *testing.T) {
	tests := []struct {
		amount, currency string
		valid            bool
	}{
		{amount: "1", currency: "ARS", valid: true},
		{amount: "1.23", currency: "USD", valid: true},
		{amount: "1.234", currency: "ARS", valid: false},
		{amount: "0.12345678", currency: "USDT", valid: true},
		{amount: "0.123456789", currency: "USDT", valid: false},
		{amount: "0", currency: "USDT", valid: false},
		{amount: "1e2", currency: "USD", valid: false},
		{amount: "-1", currency: "USD", valid: false},
	}
	for _, test := range tests {
		err := ValidateMoney(test.amount, test.currency, "amount", "currency")
		if (err == nil) != test.valid {
			t.Fatalf("ValidateMoney(%q,%q) error=%v valid=%v", test.amount, test.currency, err, test.valid)
		}
	}
}

func TestValidateMoneyRejectsUnknownCurrency(t *testing.T) {
	err := ValidateMoney("1.00", "XYZ", "amount", "currency")
	var unsupported *UnsupportedCurrencyError
	if !errors.As(err, &unsupported) || unsupported.Field != "currency" || unsupported.Currency != "XYZ" {
		t.Fatalf("error=%#v", err)
	}
}

func TestAssetCatalogHasExpectedProductionBaseline(t *testing.T) {
	for _, code := range []string{"ARS", "BRL", "EUR", "MXN", "USD", "VES", "USDT"} {
		asset, ok := Asset(code)
		if !ok {
			t.Fatalf("asset %s missing", code)
		}
		expected := 2
		if code == "USDT" {
			expected = 8
		}
		if asset.Decimals != expected {
			t.Fatalf("asset %s decimals=%d", code, asset.Decimals)
		}
	}
}
