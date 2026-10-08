package dinacore

import "testing"

func TestNormalizeBalanceAmount(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "postgres scale padding", input: "29550.000000000000000000", want: "29550"},
		{name: "preserves meaningful decimals", input: "221296.995000000000000000", want: "221296.995"},
		{name: "eight decimals", input: "0.12345678", want: "0.12345678"},
		{name: "leading zeroes", input: "00012.3400", want: "12.34"},
		{name: "more than eight meaningful decimals", input: "1.123456789", wantErr: true},
		{name: "zero", input: "0.000000000000000000", wantErr: true},
		{name: "negative", input: "-1.00", wantErr: true},
		{name: "invalid", input: "12x.30", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeBalanceAmount(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeBalanceAmount(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeBalanceAmount(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
