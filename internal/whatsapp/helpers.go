package whatsapp

import (
	"fmt"
	"strings"
)

// FormatJIDForLog returns obfuscated JID for logging (last 4 digits hidden).
func FormatJIDForLog(phone string) string {
	if len(phone) <= 4 {
		return phone
	}
	return phone[:len(phone)-4] + "****"
}

// Ensure helper is used.
var _ = strings.Contains
var _ = fmt.Sprintf
