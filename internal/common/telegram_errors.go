package common

import (
	"strings"
	"time"

	"github.com/gotd/td/tgerr"
)

// Telegram error classification shared by cascade (business logic) and
// telegram (integration) without creating an import cycle.

// FloodWaitDuration returns how long Telegram asks us to wait before
// retrying. The wait MUST be honored in full — sleeping less and retrying
// extends the ban and can get the auth key revoked (401).
func FloodWaitDuration(err error) (time.Duration, bool) {
	if err == nil {
		return 0, false
	}
	if d, ok := tgerr.AsFloodWait(err); ok {
		return d, true
	}
	// Fallback for wrapped/legacy formats like "FLOOD_WAIT (110)".
	if d, ok := parseFloodWaitParens(err.Error()); ok {
		return d, true
	}
	return 0, false
}

// IsTelegramAuthError reports fatal session errors: the auth key is dead,
// the user must log in again (QR/code). Retrying sends is pointless.
func IsTelegramAuthError(err error) bool {
	if err == nil {
		return false
	}
	if rpcErr, ok := tgerr.As(err); ok {
		if rpcErr.Code == 401 {
			return true
		}
	}
	msg := strings.ToUpper(err.Error())
	for _, s := range []string{
		"AUTH_KEY_UNREGISTERED",
		"AUTH_KEY_INVALID",
		"SESSION_REVOKED",
		"USER_DEACTIVATED_BAN",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// IsTelegramSkippable reports per-contact errors that will not succeed on
// retry: privacy blocks, deleted accounts, missing users. Skip the contact,
// do not hammer the API.
func IsTelegramSkippable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	for _, s := range []string{
		"PRIVACY_PREMIUM_REQUIRED",
		"USER_IS_BLOCKED",
		"USER_DEACTIVATED",
		"USER_NOT_MUTUAL",
		"PHONE_NOT_OCCUPIED",
		"USER_NOT_FOUND",
		"INPUT_USER_DEACTIVATED",
		"PEER_FLOOD",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// IsTelegramNotFound reports that the phone has no Telegram user (or hides
// it via privacy). Callers may fall back to ImportContacts in this case.
func IsTelegramNotFound(err error) bool {
	if err == nil {
		return false
	}
	if tgerr.Is(err, "PHONE_NOT_OCCUPIED") {
		return true
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "PHONE_NOT_OCCUPIED")
}

// parseFloodWaitParens parses legacy "FLOOD_WAIT (110)" style messages.
func parseFloodWaitParens(msg string) (time.Duration, bool) {
	upper := strings.ToUpper(msg)
	idx := strings.Index(upper, "FLOOD_WAIT")
	if idx < 0 {
		return 0, false
	}
	rest := upper[idx+len("FLOOD_WAIT"):]
	start := strings.Index(rest, "(")
	end := strings.Index(rest, ")")
	if start < 0 || end < 0 || end <= start+1 {
		return 0, false
	}
	var n int
	for _, r := range rest[start+1 : end] {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return time.Duration(n) * time.Second, true
}
