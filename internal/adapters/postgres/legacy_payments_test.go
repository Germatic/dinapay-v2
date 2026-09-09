package postgres

import "testing"

func TestLegacyBinancePaymentDataPreservesProviderAlternatives(t *testing.T) {
	got := legacyPaymentData("binancepay", "https://app.binance.com/pay/1", "binance://qr/1", "")
	if got["type"] != "redirect" {
		t.Fatalf("type = %v", got["type"])
	}
	redirect := got["redirect"].(map[string]any)
	links := redirect["links"].(map[string]any)
	if links["universal"] != "https://app.binance.com/pay/1" {
		t.Fatalf("links = %#v", links)
	}
	qr := redirect["qr"].(map[string]any)
	if qr["content"] != "binance://qr/1" {
		t.Fatalf("qr = %#v", qr)
	}
}

func TestLegacyBankTransferDoesNotInventReference(t *testing.T) {
	got := legacyPaymentData("coinag", "", "", "")
	bank := got["bankTransfer"].(map[string]any)
	if _, exists := bank["transferReference"]; exists {
		t.Fatalf("unexpected reference: %#v", bank)
	}
}
