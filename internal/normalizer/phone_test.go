package normalizer

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"+380991234567", "+380991234567", false},
		{"0991234567", "+380991234567", false},
		{" 099 123-45-67 ", "+380991234567", false},
		{"380991234567", "+380991234567", false},
		{"", "", true},
		{"123", "", true},
	}
	for _, tt := range tests {
		got, err := Normalize(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("Normalize(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("Normalize(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}
