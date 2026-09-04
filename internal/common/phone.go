package common

import (
	"strings"
)

// CleanPhone strips formatting: spaces, dashes, parentheses, plus.
// "+380 99 123-45-67" -> "380991234567", " (099) 123 " -> "099123"
func CleanPhone(phone string) string {
	phone = strings.TrimSpace(phone)
	phone = strings.TrimPrefix(phone, "+")
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")
	return phone
}

// CleanPhoneE164 returns phone with + prefix for E.164, after cleaning.
func CleanPhoneE164(phone string) string {
	cleaned := CleanPhone(phone)
	if cleaned == "" {
		return ""
	}
	return "+" + cleaned
}
