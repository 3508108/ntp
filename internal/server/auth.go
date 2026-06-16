// Package server: SHA-256 перевірка пароля для критичних ендпоінтів.
package server

import "crypto/sha256"
import "encoding/hex"

// PwdHash — SHA-256 хеш пароля, як у Python server.py:
//   _PWD_HASH = "acfb20a373bc35f4f6dde55ec29f7f91fd3078ad5192ff1e1b9a02326a4bcc1c"
const PwdHash = "acfb20a373bc35f4f6dde55ec29f7f91fd3078ad5192ff1e1b9a02326a4bcc1c"

// AuthOK повертає true, якщо SHA-256(password) == PwdHash (як Python `hashlib.sha256(...).hexdigest()`).
func AuthOK(password string) bool {
	if password == "" {
		return false
	}
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:]) == PwdHash
}
