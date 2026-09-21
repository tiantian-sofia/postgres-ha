package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordMatchesMD5(t *testing.T) {
	// md5 verifier: md5(password + username), computed independently
	ui := UserInfo{
		Username:     "op-user",
		PasswordHash: "md5626b3dc1b191d9b95bd2f7cac06b3ef9",
	}

	ok, err := ui.PasswordMatches("p'ass'word")
	require.NoError(t, err)
	assert.True(t, ok, "correct password must verify")

	ok, err = ui.PasswordMatches("wrong-password")
	require.NoError(t, err)
	assert.False(t, ok, "wrong password must not verify")
}

func TestPasswordMatchesMD5IsUsernameScoped(t *testing.T) {
	// the same password under a different role name must not verify: the
	// md5 verifier embeds the username
	ui := UserInfo{
		Username:     "someone-else",
		PasswordHash: "md5626b3dc1b191d9b95bd2f7cac06b3ef9",
	}

	ok, err := ui.PasswordMatches("p'ass'word")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestPasswordMatchesSCRAMSHA256(t *testing.T) {
	// SCRAM-SHA-256 verifier generated independently of the package code:
	// PBKDF2-HMAC-SHA256(password, salt, 4096), StoredKey =
	// SHA256(HMAC(SaltedPassword, "Client Key"))
	ui := UserInfo{
		Username: "op-user",
		PasswordHash: "SCRAM-SHA-256$4096:AAECAwQFBgcICQoLDA0ODw==" +
			"$wTzbCK4zOWY+VaQypzfw52PI1U3F/OAu1U86K9G4iHc=" +
			":fyiFKGvB3K8TXYv9ygOCsprYKo48pZSSiSQ0BsdtQPg=",
	}

	ok, err := ui.PasswordMatches("p'ass'word")
	require.NoError(t, err)
	assert.True(t, ok, "PG14-style scram verifier must verify a matching password")

	ok, err = ui.PasswordMatches("not-it")
	require.NoError(t, err)
	assert.False(t, ok, "wrong password must not verify")
}

func TestPasswordMatchesUnknownFormatIsAnError(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"empty hash", ""},
		{"unknown scheme", "SCRAM-SHA-512$4096:AAAA$AAAA:AAAA"},
		{"malformed md5", "md5deadbeef"},
		{"malformed scram", "SCRAM-SHA-256$garbage"},
		{"bad iterations", "SCRAM-SHA-256$0:AAECAwQFBgcICQoLDA0ODw==$wTzbCK4zOWY+VaQypzfw52PI1U3F/OAu1U86K9G4iHc=:fyiFKGvB3K8TXYv9ygOCsprYKo48pZSSiSQ0BsdtQPg="},
		{"bad base64 keys", "SCRAM-SHA-256$4096:AAECAwQFBgcICQoLDA0ODw==$notbase64:alsonotbase64"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := UserInfo{Username: "op-user", PasswordHash: tc.hash}
			ok, err := ui.PasswordMatches("anything")
			assert.Error(t, err, "unparseable/unknown verifiers must be reported, not silently treated as a mismatch")
			assert.False(t, ok)
		})
	}
}

func TestPBKDF2HMACSHA256(t *testing.T) {
	// RFC 7914 PBKDF2-HMAC-SHA-256 test vector, c=4096
	salt := []byte("SodiumChloride")
	key := pbkdf2HMACSHA256([]byte("passwd"), salt, 4096, 64)

	expected := "1b2a47f1588e47720e9f453639e410" +
		"606dafbede32abdfde279db6e3ed2f" +
		"ca32fce73a0b7d83b9fcb1e96f91e" +
		"e2721ca063ef6e2979ac8a5116cb8" +
		"c64677cdf6"
	assert.Equal(t, expected, toHex(key))
}

func toHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0xf]
	}
	return string(out)
}
