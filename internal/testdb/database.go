// Package testdb supplies isolated databases for the shared backend test suite.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"
)

func uniqueName() string {
	var value [10]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return "agentuse_test_" + hex.EncodeToString(value[:])
}

// Each test owns a database. External suites use an account allowed to create
// databases; ordinary go test continues to use an in-memory SQLite database.
func Config(t *testing.T) (string, string) {
	t.Helper()
	driver, dsn := os.Getenv("AGENTUSE_TEST_DB_DRIVER"), os.Getenv("AGENTUSE_TEST_DB_DSN")
	if driver == "" || driver == "sqlite" {
		return "sqlite", ":memory:"
	}
	name := uniqueName()
	var adminDriver, databaseDSN string
	switch driver {
	case "mysql":
		parsed, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal("invalid test MySQL DSN")
		}
		adminDriver = "mysql"
		parsed.DBName = name
		databaseDSN = parsed.FormatDSN()
	case "postgres":
		parsed, err := url.Parse(dsn)
		if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
			t.Fatal("test PostgreSQL DSN must be a postgres URL")
		}
		adminDriver = "pgx"
		parsed.Path = "/" + name
		databaseDSN = parsed.String()
	default:
		t.Fatalf("unsupported test database driver %q", driver)
	}
	admin, err := sql.Open(adminDriver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		query := "DROP DATABASE " + name
		if driver == "postgres" {
			query += " WITH (FORCE)"
		}
		if _, err := admin.Exec(query); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	return driver, databaseDSN
}

// RejectWrite injects a real database failure, so transaction rollback tests
// exercise the same behaviour on all three engines rather than skipping cases.
func RejectWrite(t *testing.T, db *gorm.DB, table, event, condition, message string) func() {
	t.Helper()
	name := uniqueName()
	message = strings.ReplaceAll(message, "'", "''")
	var create, drop []string
	switch db.Dialector.Name() {
	case "postgres":
		create = []string{
			fmt.Sprintf("CREATE FUNCTION %s_fn() RETURNS trigger AS $$ BEGIN IF %s THEN RAISE EXCEPTION '%s'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql", name, condition, message),
			fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION %s_fn()", name, event, table, name),
		}
		drop = []string{"DROP TRIGGER IF EXISTS " + name + " ON " + table, "DROP FUNCTION IF EXISTS " + name + "_fn()"}
	case "mysql":
		create = []string{fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s FOR EACH ROW BEGIN IF %s THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='%s'; END IF; END", name, event, table, condition, message)}
		drop = []string{"DROP TRIGGER IF EXISTS " + name}
	default:
		create = []string{fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s WHEN %s BEGIN SELECT RAISE(FAIL,'%s'); END", name, event, table, condition, message)}
		drop = []string{"DROP TRIGGER IF EXISTS " + name}
	}
	for _, statement := range create {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			for _, statement := range drop {
				if err := db.Exec(statement).Error; err != nil {
					t.Error(err)
				}
			}
		})
	}
	t.Cleanup(restore)
	return restore
}
