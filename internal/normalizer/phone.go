package normalizer

import (
	"fmt"
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// Normalize converts any +380 input to E.164 (+380XXXXXXXXX).
// Accepts "099 123-45-67", "(099)1234567", "380991234567".
func Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty phone")
	}
	// Default region UA for numbers without +.
	num, err := phonenumbers.Parse(raw, "UA")
	if err != nil {
		return "", fmt.Errorf("parse phone: %w", err)
	}
	if !phonenumbers.IsValidNumber(num) {
		return "", fmt.Errorf("invalid phone: %s", raw)
	}
	return phonenumbers.Format(num, phonenumbers.E164), nil
}
