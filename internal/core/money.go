package core

import (
	"fmt"
	"regexp"
	"strings"
)

type AssetType string

const (
	AssetFiat   AssetType = "fiat"
	AssetCrypto AssetType = "crypto"
)

type AssetDefinition struct {
	Code     string
	Type     AssetType
	Decimals int
}

// AssetCatalogVersion changes whenever the built-in monetary contract changes.
// It is intentionally static for the production baseline; a later control-plane
// implementation can publish the same model without changing validation rules.
const AssetCatalogVersion = "2026-09-21"

var assetCatalog = map[string]AssetDefinition{
	"ARS":  {Code: "ARS", Type: AssetFiat, Decimals: 2},
	"BRL":  {Code: "BRL", Type: AssetFiat, Decimals: 2},
	"EUR":  {Code: "EUR", Type: AssetFiat, Decimals: 2},
	"MXN":  {Code: "MXN", Type: AssetFiat, Decimals: 2},
	"USD":  {Code: "USD", Type: AssetFiat, Decimals: 2},
	"VES":  {Code: "VES", Type: AssetFiat, Decimals: 2},
	"USDT": {Code: "USDT", Type: AssetCrypto, Decimals: 8},
}

var plainDecimalPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

type UnsupportedCurrencyError struct {
	Field    string
	Currency string
}

func (e *UnsupportedCurrencyError) Error() string { return e.Field + " is not supported" }

func Asset(code string) (AssetDefinition, bool) {
	asset, ok := assetCatalog[code]
	return asset, ok
}

func ValidateCurrency(currency, field string) (AssetDefinition, error) {
	asset, ok := Asset(currency)
	if !ok {
		return AssetDefinition{}, &UnsupportedCurrencyError{Field: field, Currency: currency}
	}
	return asset, nil
}

func ValidateMoney(amount, currency, amountField, currencyField string) error {
	asset, err := ValidateCurrency(currency, currencyField)
	if err != nil {
		return err
	}
	if len(amount) == 0 || len(amount) > 64 || !plainDecimalPattern.MatchString(amount) || fractionalDigits(amount) > asset.Decimals || !hasNonZeroDigit(amount) {
		return &ValidationError{
			Field:   amountField,
			Rule:    "format",
			Message: fmt.Sprintf("%s must be a positive decimal with at most %d fractional digits", amountField, asset.Decimals),
		}
	}
	return nil
}

func fractionalDigits(value string) int {
	if separator := strings.IndexByte(value, '.'); separator >= 0 {
		return len(value) - separator - 1
	}
	return 0
}

func hasNonZeroDigit(value string) bool {
	return strings.ContainsAny(value, "123456789")
}
