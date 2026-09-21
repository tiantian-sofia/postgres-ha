package admin

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PasswordMatches reports whether password matches the verifier stored in
// PasswordHash.
//
// PostgreSQL can store password verifiers in two formats (see
// pg_authid.rolpassword):
//
//   - md5<hash>  : md5 of password followed by the role name, used by the
//     md5 authentication method and still produced for pre-existing roles
//   - SCRAM-SHA-256$<iterations>:<salt>$<stored key>:<server key> : the
//     default produced since PostgreSQL 10 and verified when
//     password_encryption is scram-sha-256 (the PG14 default)
//
// The boolean result is only meaningful when err is nil. When the stored
// verifier is missing or in an unrecognized/undecodable format, ok is
// false and err explains why; callers must treat that as "unknown" and
// leave the password untouched rather than resetting it on every start.
func (ui UserInfo) PasswordMatches(password string) (bool, error) {
	stored := ui.PasswordHash

	switch {
	case strings.HasPrefix(stored, "md5"):
		return md5PasswordMatches(stored, ui.Username, password)
	case strings.HasPrefix(stored, "SCRAM-SHA-256$"):
		return scramSHA256PasswordMatches(stored, password)
	case stored == "":
		return false, errors.New("stored password verifier is empty")
	default:
		return false, fmt.Errorf("unrecognized password verifier format: %q", verifierPrefix(stored))
	}
}

func verifierPrefix(stored string) string {
	if idx := strings.IndexByte(stored, '$'); idx >= 0 {
		return stored[:idx]
	}
	return stored
}

func md5PasswordMatches(stored, username, password string) (bool, error) {
	hexDigest := strings.TrimPrefix(stored, "md5")
	if len(hexDigest) != md5.Size*2 {
		return false, fmt.Errorf("malformed md5 password verifier: %q", stored)
	}
	if _, err := hex.DecodeString(hexDigest); err != nil {
		return false, fmt.Errorf("malformed md5 password verifier: %q", stored)
	}

	sum := md5.Sum([]byte(password + username))
	calculated := "md5" + hex.EncodeToString(sum[:])
	return hmac.Equal([]byte(calculated), []byte(stored)), nil
}

// scramVerifier holds the decoded parts of a SCRAM-SHA-256 stored
// verifier.
type scramVerifier struct {
	iterations int
	salt       []byte
	storedKey  []byte
}

func scramSHA256PasswordMatches(stored, password string) (bool, error) {
	verifier, err := parseScramVerifier(stored)
	if err != nil {
		return false, err
	}

	// RFC 5802, §3 / RFC 7677: StoredKey is H(SaltedPassword, "Client Key")
	// hashed with SHA-256, where SaltedPassword is the PBKDF2 of the
	// password and salt. Comparing StoredKey is sufficient to verify the
	// password without running the SASL exchange.
	saltedPassword := pbkdf2HMACSHA256([]byte(password), verifier.salt, verifier.iterations, sha256.Size)
	clientKey := hmacSHA256(saltedPassword, []byte("Client Key"))
	calculatedStoredKey := sha256.Sum256(clientKey)

	return hmac.Equal(calculatedStoredKey[:], verifier.storedKey), nil
}

func parseScramVerifier(stored string) (*scramVerifier, error) {
	const prefix = "SCRAM-SHA-256$"
	rest := strings.TrimPrefix(stored, prefix)

	parts := strings.SplitN(rest, "$", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed SCRAM password verifier: %q", stored)
	}

	iterSalt := strings.SplitN(parts[0], ":", 2)
	if len(iterSalt) != 2 {
		return nil, fmt.Errorf("malformed SCRAM password verifier: %q", stored)
	}

	iterations, err := strconv.Atoi(iterSalt[0])
	if err != nil || iterations <= 0 {
		return nil, fmt.Errorf("invalid iteration count in SCRAM password verifier: %q", stored)
	}

	salt, err := base64.StdEncoding.DecodeString(iterSalt[1])
	if err != nil {
		return nil, fmt.Errorf("invalid salt in SCRAM password verifier: %w", err)
	}

	keys := strings.Split(parts[1], ":")
	if len(keys) != 2 {
		return nil, fmt.Errorf("malformed SCRAM password verifier: %q", stored)
	}

	storedKey, err := base64.StdEncoding.DecodeString(keys[0])
	if err != nil {
		return nil, fmt.Errorf("invalid stored key in SCRAM password verifier: %w", err)
	}

	// The server key is not needed for password verification but must be
	// present and decodable for the verifier to be considered valid.
	if _, err := base64.StdEncoding.DecodeString(keys[1]); err != nil {
		return nil, fmt.Errorf("invalid server key in SCRAM password verifier: %w", err)
	}

	return &scramVerifier{
		iterations: iterations,
		salt:       salt,
		storedKey:  storedKey,
	}, nil
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// pbkdf2HMACSHA256 derives a key from password, salt and iterations using
// PBKDF2 with HMAC-SHA-256, as specified in RFC 2898 (PKCS #5) and used by
// SCRAM-SHA-256 (RFC 5802).
func pbkdf2HMACSHA256(password, salt []byte, iterations, keyLen int) []byte {
	hashLen := sha256.Size
	numBlocks := (keyLen + hashLen - 1) / hashLen

	derived := make([]byte, 0, numBlocks*hashLen)
	var block [4]byte
	for blockIndex := 1; blockIndex <= numBlocks; blockIndex++ {
		block[0] = byte(blockIndex >> 24)
		block[1] = byte(blockIndex >> 16)
		block[2] = byte(blockIndex >> 8)
		block[3] = byte(blockIndex)

		prf := hmac.New(sha256.New, password)
		prf.Write(salt)
		prf.Write(block[:])
		ux := prf.Sum(nil)

		te := make([]byte, len(ux))
		copy(te, ux)

		for i := 1; i < iterations; i++ {
			prf = hmac.New(sha256.New, password)
			prf.Write(ux)
			ux = prf.Sum(nil)
			for j := range te {
				te[j] ^= ux[j]
			}
		}

		derived = append(derived, te...)
	}

	return derived[:keyLen]
}
