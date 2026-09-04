package common

import "github.com/skip2/go-qrcode"

// EncodeQR returns 256x256 PNG for given content, Medium level.
// Wrapper to deduplicate qrcode.Encode calls across whatsapp/telegram.
func EncodeQR(content string) ([]byte, error) {
	return qrcode.Encode(content, qrcode.Medium, 256)
}
