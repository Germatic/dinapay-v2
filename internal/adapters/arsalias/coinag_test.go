package arsalias

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCoinagResolvesAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
		case "/coelsapsp/v1/Alias/mi.alias":
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"cuenta": map[string]any{"cbu": "0070327530004025541644"}, "titulares": []map[string]any{{"cuit": "20221370075", "nombre": "Gerardo Ratto"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	resolver, err := NewCoinag(CoinagConfig{BaseURL: server.URL, TokenURL: server.URL + "/token", ClientID: "id", ClientSecret: "secret", Username: "user", Password: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolver.ResolveAlias(t.Context(), "mi.alias")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountNumber != "0070327530004025541644" || got.TaxID != "20221370075" {
		t.Fatalf("resolved=%#v", got)
	}
}
