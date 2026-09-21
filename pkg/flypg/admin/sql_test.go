package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuoteIdentifier(t *testing.T) {
	cases := map[string]string{
		"app":             `"app"`,
		"my-db":           `"my-db"`,
		"my db":           `"my db"`,
		`weird"name`:      `"weird""name"`,
		`a"b"c`:           `"a""b""c"`,
		"123startsnumber": `"123startsnumber"`,
		"":                `""`,
		// identifiers must never end up escaped like Go strings
		"tab\tname": "\"tab\tname\"",
	}

	for input, expected := range cases {
		assert.Equal(t, expected, quoteIdentifier(input), "input=%q", input)
	}
}

func TestQuoteLiteral(t *testing.T) {
	cases := map[string]string{
		"secret":          "E'secret'",
		"p'ass'word":      "E'p''ass''word'",
		`back\slash`:      `E'back\\slash'`,
		`mix'ed\stuff`:    `E'mix''ed\\stuff'`,
		"":                "E''",
		"semi;colon":      "E'semi;colon'",
		"-- line comment": "E'-- line comment'",
	}

	for input, expected := range cases {
		assert.Equal(t, expected, quoteLiteral(input), "input=%q", input)
	}

	// A crafted password must not be able to break out of the literal.
	evil := `x'; DROP USER postgres; --`
	assert.Equal(t, `E'x''; DROP USER postgres; --'`, quoteLiteral(evil))
}

func TestCreateUserSQL(t *testing.T) {
	assert.Equal(t,
		`CREATE USER "my-user" WITH LOGIN PASSWORD E'secret'`,
		createUserSQL("my-user", "secret"))

	assert.Equal(t,
		`CREATE USER "app" WITH LOGIN PASSWORD E'p''ass'`,
		createUserSQL("app", "p'ass"))
}

func TestChangePasswordSQL(t *testing.T) {
	assert.Equal(t,
		`ALTER USER "my-user" WITH LOGIN PASSWORD E'n3w''pass'`,
		changePasswordSQL("my-user", "n3w'pass"))
}

func TestGrantAndRevokeSuperuserSQL(t *testing.T) {
	assert.Equal(t, `ALTER USER "flypgadmin" WITH SUPERUSER`,
		grantSuperuserSQL("flypgadmin"))
	assert.Equal(t, `ALTER USER "flypgadmin" WITH NOSUPERUSER`,
		revokeSuperuserSQL("flypgadmin"))
}

func TestGrantAndRevokeReplicationSQL(t *testing.T) {
	assert.Equal(t, `ALTER USER "repli-cator" WITH REPLICATION`,
		grantReplicationSQL("repli-cator"))
	assert.Equal(t, `ALTER USER "repli-cator" WITH NOREPLICATION`,
		revokeReplicationSQL("repli-cator"))
}

func TestDeleteUserSQL(t *testing.T) {
	assert.Equal(t, `DROP USER "my-user"`, deleteUserSQL("my-user"))
}

func TestCreateAndDeleteDatabaseSQL(t *testing.T) {
	assert.Equal(t, `CREATE DATABASE "my-db"`, createDatabaseSQL("my-db"))
	assert.Equal(t, `DROP DATABASE "my-db"`, deleteDatabaseSQL("my-db"))
}

func TestGrantAccessSQL(t *testing.T) {
	// first argument is the database, second the role being granted
	assert.Equal(t,
		`GRANT ALL PRIVILEGES ON DATABASE "my-db" TO "my-user"`,
		grantAccessSQL("my-db", "my-user"))

	assert.Equal(t,
		`REVOKE ALL PRIVILEGES ON DATABASE "my-db" FROM "my-user"`,
		revokeAccessSQL("my-db", "my-user"))
}

func TestHyphenAndQuoteNamesAreQuotedEverywhere(t *testing.T) {
	// regression: hyphenated names used to produce a syntax error, and a
	// name containing a double quote used to be able to terminate the
	// identifier.
	assert.Equal(t, `DROP DATABASE "a-b"`, deleteDatabaseSQL("a-b"))
	assert.Equal(t, `CREATE DATABASE "a-b"`, createDatabaseSQL("a-b"))
	assert.Equal(t, `DROP USER "a-b"`, deleteUserSQL("a-b"))
	assert.Equal(t,
		`GRANT ALL PRIVILEGES ON DATABASE "a""b" TO "c-d"`,
		grantAccessSQL(`a"b`, "c-d"))
}
