package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQuoteIdentifier(t *testing.T) {
	cases := map[string]string{
		"app":        `"app"`,
		"my-user":    `"my-user"`,
		"my-db-1":    `"my-db-1"`,
		`weird"name`: `"weird""name"`,
		"":           `""`,
		"CAPITAL":    `"CAPITAL"`,
	}
	for input, want := range cases {
		assert.Equal(t, want, quoteIdentifier(input))
	}
}

func TestQuoteLiteral(t *testing.T) {
	cases := map[string]string{
		"secret":          `'secret'`,
		"p@ss w0rd!":      `'p@ss w0rd!'`,
		"it's a secret":   `'it''s a secret'`,
		"a'; DROP ROLE x": `'a''; DROP ROLE x'`,
		"":                `''`,
	}
	for input, want := range cases {
		assert.Equal(t, want, quoteLiteral(input))
	}
}

func TestCreateUserSQL(t *testing.T) {
	assert.Equal(t,
		`CREATE USER "my-user" WITH LOGIN PASSWORD 'p@ss'`,
		CreateUserSQL("my-user", "p@ss"))
	assert.Equal(t,
		`CREATE USER "bob" WITH LOGIN PASSWORD 'a''b'`,
		CreateUserSQL("bob", "a'b"))
}

func TestCreateSuperuserSQL(t *testing.T) {
	assert.Equal(t,
		`CREATE USER "flypgadmin" WITH SUPERUSER LOGIN PASSWORD 's3cret!'`,
		CreateSuperuserSQL("flypgadmin", "s3cret!"))
}

func TestCreateReplicationUserSQL(t *testing.T) {
	assert.Equal(t,
		`CREATE USER "repl-user" WITH REPLICATION PASSWORD 's3cret!'`,
		CreateReplicationUserSQL("repl-user", "s3cret!"))
}

func TestAlterAndChangePasswordSQL(t *testing.T) {
	assert.Equal(t,
		`ALTER USER "my-user" WITH PASSWORD 'a''b'`,
		AlterUserPasswordSQL("my-user", "a'b"))
	assert.Equal(t,
		`ALTER USER "my-user" WITH LOGIN PASSWORD 'a''b'`,
		ChangePasswordSQL("my-user", "a'b"))
}

func TestRoleSQL(t *testing.T) {
	assert.Equal(t, `ALTER USER "my-user" WITH SUPERUSER`, GrantSuperuserSQL("my-user"))
	assert.Equal(t, `ALTER USER "my-user" WITH NOSUPERUSER`, RevokeSuperuserSQL("my-user"))
	assert.Equal(t, `ALTER USER "my-user" WITH REPLICATION`, GrantReplicationSQL("my-user"))
}

func TestDropUserSQL(t *testing.T) {
	assert.Equal(t, `DROP USER "my-user"`, DropUserSQL("my-user", false))
	assert.Equal(t, `DROP USER IF EXISTS "my-user"`, DropUserSQL("my-user", true))
}

func TestDatabaseSQL(t *testing.T) {
	assert.Equal(t, `CREATE DATABASE "my-db"`, CreateDatabaseSQL("my-db"))
	assert.Equal(t, `DROP DATABASE "my-db"`, DropDatabaseSQL("my-db"))
}

func TestGrantRevokeAccessSQL(t *testing.T) {
	assert.Equal(t,
		`GRANT ALL PRIVILEGES ON DATABASE "my-db" TO "my-user"`,
		GrantAccessSQL("my-db", "my-user"))
	assert.Equal(t,
		`REVOKE ALL PRIVILEGES ON DATABASE "my-db" FROM "my-user"`,
		RevokeAccessSQL("my-db", "my-user"))
}

func TestSetDatabaseReadonlySQL(t *testing.T) {
	assert.Equal(t,
		`ALTER DATABASE "my-db" SET default_transaction_read_only=on`,
		SetDatabaseReadonlySQL("my-db", true))
	assert.Equal(t,
		`ALTER DATABASE "my-db" SET default_transaction_read_only=off`,
		SetDatabaseReadonlySQL("my-db", false))
}

func TestSQLIsNotInjectable(t *testing.T) {
	// A crafted username containing quotes and statement terminators must end
	// up fully inside a single quoted identifier.
	sql := CreateUserSQL(`evil"; DROP ROLE postgres; --`, `x'; DROP ROLE postgres; --`)
	assert.Contains(t, sql, `"evil""; DROP ROLE postgres; --"`)
	assert.Contains(t, sql, `'x''; DROP ROLE postgres; --'`)
	// Every semicolon in the crafted payload must sit inside a quoted token,
	// i.e. there is no statement terminator at the top grammar level.
	assert.False(t, hasTopLevelTerminator(sql))
}

// hasTopLevelTerminator reports whether sql contains a semicolon outside of
// double-quoted identifiers and single-quoted string literals.
func hasTopLevelTerminator(sql string) bool {
	inIdentifier, inLiteral := false, false
	runes := []rune(sql)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '"':
			if !inLiteral {
				if inIdentifier && i+1 < len(runes) && runes[i+1] == '"' {
					i++ // escaped quote
				} else {
					inIdentifier = !inIdentifier
				}
			}
		case '\'':
			if !inIdentifier {
				if inLiteral && i+1 < len(runes) && runes[i+1] == '\'' {
					i++ // escaped quote
				} else {
					inLiteral = !inLiteral
				}
			}
		case ';':
			if !inIdentifier && !inLiteral {
				return true
			}
		}
	}
	return false
}
