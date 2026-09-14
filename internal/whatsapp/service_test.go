package whatsapp

import (
	"errors"
	"testing"
)

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

func TestIsNoLIDError(t *testing.T) {
	lidErr := errors.New(`send media "zarahuvannia.png": no LID found for 380958419473@s.whatsapp.net from server`)
	if !isNoLIDError(lidErr) {
		t.Error("must detect no-LID error from server")
	}
	if isNoLIDError(errors.New("send message: timeout")) {
		t.Error("timeout must not count as no-LID")
	}
	if isNoLIDError(nil) {
		t.Error("nil must not count as no-LID")
	}
}

func TestSendWithoutClient(t *testing.T) {
	s, _ := NewWithDB("file::memory:?cache=shared")
	if err := s.sendConvertedText("+380991234567", "hi"); err == nil {
		t.Error("send without client must fail, not panic")
	}
	if _, err := s.IsOnWhatsApp("+380991234567"); err == nil {
		t.Error("IsOnWhatsApp without client must fail, not panic")
	}
	if err := s.SendMedia("+380991234567", "cap", nil); err == nil {
		t.Error("SendMedia nil attachment must fail, not panic")
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
