package admin

import (
	"fmt"
	"strings"
)

// quoteIdentifier quotes name for use as a PostgreSQL identifier (a role or
// database name in DDL). It is the Go equivalent of the server-side
// quote_ident function: the name is wrapped in double quotes and every
// embedded double quote is doubled. No other escaping is valid for
// identifiers, which is why strconv.Quote (%q) must never be used here.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteLiteral quotes value for use as a SQL string literal, mirroring the
// server-side quote_literal function (E'...' form): single quotes are
// doubled and backslashes are doubled. Using a properly quoted literal
// keeps values such as a password containing a single quote from breaking
// out of the string.
func quoteLiteral(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `''`)
	return "E'" + escaped + "'"
}

func createUserSQL(username, password string) string {
	return fmt.Sprintf("CREATE USER %s WITH LOGIN PASSWORD %s",
		quoteIdentifier(username), quoteLiteral(password))
}

func grantSuperuserSQL(username string) string {
	return fmt.Sprintf("ALTER USER %s WITH SUPERUSER", quoteIdentifier(username))
}

func grantReplicationSQL(username string) string {
	return fmt.Sprintf("ALTER USER %s WITH REPLICATION", quoteIdentifier(username))
}

func revokeSuperuserSQL(username string) string {
	return fmt.Sprintf("ALTER USER %s WITH NOSUPERUSER", quoteIdentifier(username))
}

func revokeReplicationSQL(username string) string {
	return fmt.Sprintf("ALTER USER %s WITH NOREPLICATION", quoteIdentifier(username))
}

func changePasswordSQL(username, password string) string {
	return fmt.Sprintf("ALTER USER %s WITH LOGIN PASSWORD %s",
		quoteIdentifier(username), quoteLiteral(password))
}

func deleteUserSQL(username string) string {
	return fmt.Sprintf("DROP USER %s", quoteIdentifier(username))
}

func createDatabaseSQL(name string) string {
	return fmt.Sprintf("CREATE DATABASE %s", quoteIdentifier(name))
}

func deleteDatabaseSQL(name string) string {
	return fmt.Sprintf("DROP DATABASE %s", quoteIdentifier(name))
}

func grantAccessSQL(database, username string) string {
	return fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s",
		quoteIdentifier(database), quoteIdentifier(username))
}

func revokeAccessSQL(database, username string) string {
	return fmt.Sprintf("REVOKE ALL PRIVILEGES ON DATABASE %s FROM %s",
		quoteIdentifier(database), quoteIdentifier(username))
}
