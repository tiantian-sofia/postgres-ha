package admin

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

// PasswordMatch reports how a candidate password compares to the stored
// verifier in pg_authid.rolpassword.
type PasswordMatch int

const (
	// PasswordMatchUnknown means the stored verifier could not be
	// interpreted. Callers must leave the account untouched rather than
	// resetting its password.
	PasswordMatchUnknown PasswordMatch = iota
	PasswordMismatch
	PasswordMatchOK
)

// ComparePassword verifies password against the role's stored verifier.
// PostgreSQL 10+ defaults to scram-sha-256; older clusters (and roles created
// before the default changed) store md5 hashes. Both formats are accepted.
func (ui UserInfo) ComparePassword(password string) PasswordMatch {
	switch {
	case strings.HasPrefix(ui.PasswordHash, "md5"):
		encoded := fmt.Sprintf("md5%x", md5.Sum([]byte(password+ui.Username)))
		if subtle.ConstantTimeCompare([]byte(encoded), []byte(ui.PasswordHash)) == 1 {
			return PasswordMatchOK
		}
		return PasswordMismatch
	case strings.HasPrefix(ui.PasswordHash, "SCRAM-SHA-256$"):
		return compareSCRAMSHA256(ui.PasswordHash, password)
	default:
		return PasswordMatchUnknown
	}
}

func compareSCRAMSHA256(stored, password string) PasswordMatch {
	parts := strings.SplitN(strings.TrimPrefix(stored, "SCRAM-SHA-256$"), "$", 2)
	if len(parts) != 2 {
		return PasswordMatchUnknown
	}

	iterations, salt, err := parseSCRAMParams(parts[0])
	if err != nil || iterations <= 0 {
		return PasswordMatchUnknown
	}

	fields := strings.Split(parts[1], ":")
	if len(fields) != 2 {
		return PasswordMatchUnknown
	}

	saltedPassword := pbkdf2HMACSHA256([]byte(password), salt, iterations, sha256.Size)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	storedKey := sha256.Sum256(clientKey)
	serverKey := hmacSHA256(saltedPassword, []byte("Server Key"))

	if !hmac.Equal([]byte(fields[0]), []byte(base64.StdEncoding.EncodeToString(storedKey[:]))) {
		return PasswordMismatch
	}
	if !hmac.Equal([]byte(fields[1]), []byte(base64.StdEncoding.EncodeToString(serverKey))) {
		return PasswordMismatch
	}
	return PasswordMatchOK
}

// parseSCRAMParams parses the parameter segment of a PostgreSQL scram-sha-256
// verifier, which is stored as "iterations:salt" (both as stored in
// pg_authid.rolpassword).
func parseSCRAMParams(params string) (int, []byte, error) {
	fields := strings.SplitN(params, ":", 2)
	if len(fields) != 2 {
		return 0, nil, fmt.Errorf("invalid scram parameters %q", params)
	}

	iterations, err := strconv.Atoi(fields[0])
	if err != nil || iterations <= 0 {
		return 0, nil, fmt.Errorf("invalid scram iterations %q", fields[0])
	}

	salt, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return 0, nil, fmt.Errorf("invalid scram salt: %w", err)
	}

	return iterations, salt, nil
}

func hmacSHA256(key, message []byte) []byte {
	var mac hash.Hash = hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// pbkdf2HMACSHA256 derives a key per RFC 2898 / RFC 8018 section 5.2 using
// HMAC-SHA-256 as the pseudorandom function.
func pbkdf2HMACSHA256(password, salt []byte, iter, keyLen int) []byte {
	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	U := make([]byte, hashLen)
	T := make([]byte, hashLen)

	for block := 1; block <= numBlocks; block++ {
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)

		U = hmacSHA256(password, append(salt, buf[0], buf[1], buf[2], buf[3]))
		copy(T, U)

		for n := 2; n <= iter; n++ {
			U = hmacSHA256(password, U)
			for x := range T {
				T[x] ^= U[x]
			}
		}
		dk = append(dk, T...)
	}

	return dk[:keyLen]
}
