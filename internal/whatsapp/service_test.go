package whatsapp

import "testing"

func TestParseJID(t *testing.T) {
	tests := []struct {
		phone   string
		want    string
		wantErr bool
	}{
		{"+380991234567", "380991234567@s.whatsapp.net", false},
		{"380991234567", "380991234567@s.whatsapp.net", false},
		{" 380 99 123 45 67 ", "380991234567@s.whatsapp.net", false},
		{"+380-99-123-45-67", "380991234567@s.whatsapp.net", false},
		{"", "", true},
		{"abc", "", true},
		{"+38a991234567", "", true},
	}
	for _, tt := range tests {
		jid, err := parseJID(tt.phone)
		if (err != nil) != tt.wantErr {
			t.Fatalf("parseJID(%q) err=%v wantErr=%v", tt.phone, err, tt.wantErr)
		}
		if !tt.wantErr && jid.String() != tt.want {
			t.Errorf("parseJID(%q)=%q want %q", tt.phone, jid.String(), tt.want)
		}
	}
}

func TestServiceName(t *testing.T) {
	s, _ := NewWithDB("file::memory:?cache=shared")
	if s.Name() != "whatsapp" {
		t.Errorf("Name()=%q want whatsapp", s.Name())
	}
	if s.DBPath() == "" {
		t.Error("DBPath empty")
	}
}

func TestFormatJIDForLog(t *testing.T) {
	if got := FormatJIDForLog("+380991234567"); got != "+38099123****" {
		t.Errorf("FormatJIDForLog got %q", got)
	}
	if got := FormatJIDForLog("123"); got != "123" {
		t.Errorf("short phone should not mask, got %q", got)
	}
}
