package admin

import "strings"

// quoteIdentifier renders value as a quoted SQL identifier. Identifiers
// (user names, database names) cannot be passed as parameters, so they are
// double quoted with every embedded double quote doubled. Names containing
// NUL bytes are invalid in PostgreSQL and are rejected.
func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// quoteLiteral renders value as a quoted SQL string literal. It uses the
// standard_conforming_strings escaping (single quote doubling) which is the
// default since PostgreSQL 9.1. Passwords and other values must be rendered
// this way instead of relying on Go's %q formatting.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, `'`, `''`) + "'"
}

func CreateUserSQL(username, password string) string {
	return "CREATE USER " + quoteIdentifier(username) + " WITH LOGIN PASSWORD " + quoteLiteral(password)
}

func CreateSuperuserSQL(username, password string) string {
	return "CREATE USER " + quoteIdentifier(username) + " WITH SUPERUSER LOGIN PASSWORD " + quoteLiteral(password)
}

func CreateReplicationUserSQL(username, password string) string {
	return "CREATE USER " + quoteIdentifier(username) + " WITH REPLICATION PASSWORD " + quoteLiteral(password)
}

func AlterUserPasswordSQL(username, password string) string {
	return "ALTER USER " + quoteIdentifier(username) + " WITH PASSWORD " + quoteLiteral(password)
}

func ChangePasswordSQL(username, password string) string {
	return "ALTER USER " + quoteIdentifier(username) + " WITH LOGIN PASSWORD " + quoteLiteral(password)
}

func GrantSuperuserSQL(username string) string {
	return "ALTER USER " + quoteIdentifier(username) + " WITH SUPERUSER"
}

func RevokeSuperuserSQL(username string) string {
	return "ALTER USER " + quoteIdentifier(username) + " WITH NOSUPERUSER"
}

func GrantReplicationSQL(username string) string {
	return "ALTER USER " + quoteIdentifier(username) + " WITH REPLICATION"
}

func DropUserSQL(username string, ifExists bool) string {
	if ifExists {
		return "DROP USER IF EXISTS " + quoteIdentifier(username)
	}
	return "DROP USER " + quoteIdentifier(username)
}

func CreateDatabaseSQL(name string) string {
	return "CREATE DATABASE " + quoteIdentifier(name)
}

func DropDatabaseSQL(name string) string {
	return "DROP DATABASE " + quoteIdentifier(name)
}

func GrantAccessSQL(database, username string) string {
	return "GRANT ALL PRIVILEGES ON DATABASE " + quoteIdentifier(database) + " TO " + quoteIdentifier(username)
}

func RevokeAccessSQL(database, username string) string {
	return "REVOKE ALL PRIVILEGES ON DATABASE " + quoteIdentifier(database) + " FROM " + quoteIdentifier(username)
}

func SetDatabaseReadonlySQL(name string, enable bool) string {
	state := "off"
	if enable {
		state = "on"
	}
	return "ALTER DATABASE " + quoteIdentifier(name) + " SET default_transaction_read_only=" + state
}
