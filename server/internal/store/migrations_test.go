package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-gormigrate/gormigrate/v2"
	"github.com/memory-co/agentuse/internal/testdb"
	"gorm.io/gorm"
)

func backendConfig(t *testing.T) Config {
	t.Helper()
	driver, dsn := testdb.Config(t)
	if driver == "sqlite" {
		dsn = filepath.Join(t.TempDir(), "metadata.db")
	}
	return Config{Driver: driver, DSN: dsn}
}

func TestConcurrentOpenMigratesOnceAndReopenPreservesData(t *testing.T) {
	config := backendConfig(t)
	const count = 4
	var group sync.WaitGroup
	results := make(chan *Store, count)
	errors := make(chan error, count)
	for range count {
		group.Add(1)
		go func() { defer group.Done(); db, err := OpenConfig(config); results <- db; errors <- err }()
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
	for db := range results {
		if db != nil {
			db.Close()
		}
	}
	if t.Failed() {
		return
	}
	db, err := OpenConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.db.Create(&User{ID: "persisted", Name: "persisted", TokenHash: HashSecret("token"), CreatedAt: 1}).Error; err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var records, versions int64
	if err = db.db.Model(&User{}).Count(&records).Error; err != nil || records != 1 {
		t.Fatal(records, err)
	}
	if err = db.db.Table("schema_migrations").Count(&versions).Error; err != nil || versions != 1 {
		t.Fatal(versions, err)
	}
}

func TestVersionedMigrationPreservesDataAndRejectsUnknownVersions(t *testing.T) {
	config := backendConfig(t)
	db, err := OpenConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.db.Create(&User{ID: "u", Name: "original", TokenHash: HashSecret("token"), CreatedAt: 1}).Error; err != nil {
		t.Fatal(err)
	}
	steps := append(migrations(), &gormigrate.Migration{ID: "0002_test_column", Migrate: func(tx *gorm.DB) error {
		return tx.Exec("ALTER TABLE users ADD COLUMN migration_test_value INTEGER NOT NULL DEFAULT 0").Error
	}})
	options := *gormigrate.DefaultOptions
	options.TableName = "schema_migrations"
	options.ValidateUnknownMigrations = true
	for range 2 {
		if err = gormigrate.New(db.db, &options, steps).Migrate(); err != nil {
			t.Fatal(err)
		}
	}
	var name string
	if err = Row(context.Background(), db.db, "SELECT name FROM users WHERE id=?", "u").Scan(&name); err != nil || name != "original" {
		t.Fatal(name, err)
	}
	db.Close()
	if reopened, err := OpenConfig(config); err == nil {
		reopened.Close()
		t.Fatal("older binary accepted an unknown migration")
	} else if !errors.Is(err, gormigrate.ErrUnknownPastMigration) {
		t.Fatal(err)
	}
}

func TestBackendConstraintsPreserveNullsCaseAndRoundUniqueness(t *testing.T) {
	db, err := OpenConfig(backendConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"Alice", "alice"} {
		if err = db.db.Create(&User{ID: name, Name: name, TokenHash: HashSecret(name), CreatedAt: 1}).Error; err != nil {
			t.Fatal("case-sensitive identities changed", err)
		}
	}
	for _, id := range []string{"first", "second"} {
		if err = db.db.Create(&Message{ID: id, Seq: int64(len(id)), ConversationID: "root", AuthorRole: "user", Content: "note", CreatedAt: 1}).Error; err != nil {
			t.Fatal("NULL child authors collided", err)
		}
	}
	round := ConversationRound{ConversationID: "root", Seq: 1, StartMessageSeq: 1, CreatedAt: 1}
	if err = db.db.Create(&round).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.db.Create(&ConversationRound{ConversationID: "root", Seq: 2, StartMessageSeq: 2, CreatedAt: 1}).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("multiple active rounds accepted", err)
	}
	if err = db.db.Model(&ConversationRound{}).Where("conversation_id=? AND seq=?", "root", 1).Updates(map[string]any{"end_message_seq": 1, "finished_at": 2}).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.db.Create(&ConversationRound{ConversationID: "root", Seq: 2, StartMessageSeq: 2, CreatedAt: 2}).Error; err != nil {
		t.Fatal("closed round blocked next round", err)
	}
	path := strings.Repeat("路径/", 500) + "file.txt"
	attachment := Attachment{ID: "long-path", ConversationID: "root", RelativePath: Text(path), PathHash: HashSecret(path), Filename: "file.txt", ContentType: "text/plain", SizeBytes: 1, SHA256: HashSecret("x"), CreatedBy: "Alice", Source: "user", CreatedAt: 1}
	if err = db.db.Create(&attachment).Error; err != nil {
		t.Fatal("long Unicode path was truncated or rejected", err)
	}
	attachment.ID = "duplicate-path"
	if err = db.db.Create(&attachment).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("duplicate canonical path accepted", err)
	}
}

func TestProjectionErrorsRemainErrors(t *testing.T) {
	db, err := OpenConfig(backendConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var value int
	if err = Row(ctx, db.db, "SELECT 1").Scan(&value); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInvalidDatabaseConfigurationDoesNotEchoCredentials(t *testing.T) {
	for _, config := range []Config{
		{Driver: "mysql", DSN: "private-test-password"},
		{Driver: "postgres", DSN: "postgres://user:private-test-password@bad%host/database"},
	} {
		if db, err := OpenConfig(config); err == nil {
			db.Close()
			t.Fatal("invalid DSN accepted")
		} else if strings.Contains(err.Error(), "private-test-password") {
			t.Fatal("database error exposed credentials")
		}
	}
	if db, err := OpenConfig(Config{Driver: "unsupported"}); err == nil {
		db.Close()
		t.Fatal("unknown driver accepted")
	}
}

func TestUnversionedDatabaseIsRejectedWithoutChangingItsData(t *testing.T) {
	config := backendConfig(t)
	driver := map[string]string{"sqlite": "sqlite3", "mysql": "mysql", "postgres": "pgx"}[config.Driver]
	legacy, err := sql.Open(driver, config.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	table := "preserved_data"
	if config.Driver != "sqlite" {
		table = "sqlite_preserved_data"
	}
	if _, err = legacy.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec("INSERT INTO " + table + "(id) VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	if db, err := OpenConfig(config); err == nil {
		db.Close()
		t.Fatal("unversioned data accepted")
	} else if !strings.Contains(err.Error(), "unsupported database schema") {
		t.Fatal(err)
	}
	var count int
	if err = legacy.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 1 {
		t.Fatal("legacy data changed", count, err)
	}
}

func TestRoutingJSONProjectionHandlesAliasesAndReceiptFlags(t *testing.T) {
	db, err := OpenConfig(backendConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	message := Message{ID: "m", ConversationID: "conversation", Seq: 1, AuthorRole: "user", Content: "body", CreatedAt: 1}
	if err = db.db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	target := MessageTargets(db.db, "source", "route")
	for _, test := range []struct {
		json                string
		pending, dispatched bool
		uncertain           int
	}{
		{`{"coder":{"agent_version_id":"v","device_id":"d","harness":"h"}}`, true, false, 0},
		{`{"coder":{"agent_version_id":"v","device_id":"d","harness":"h","dispatch":{},"error":{"error":"lost","uncertain":true}}}`, true, true, 1},
		{`{"coder":{"agent_version_id":"v","device_id":"d","harness":"h","error":{"error":"rejected","uncertain":false}}}`, false, false, 0},
	} {
		if err = db.db.Model(&Message{}).Where("id=?", "m").Update("agent_targets", JSON(test.json)).Error; err != nil {
			t.Fatal(err)
		}
		var id, conversation, key, version, device, harness, content, dispatch, receipt string
		if err = Row(context.Background(), db.db, target.Columns()).Scan(&id, &conversation, &key, &version, &device, &harness, &content, &dispatch, &receipt); err != nil {
			t.Fatal(err)
		}
		if id != "m" || conversation != "conversation" || key != "coder" || version != "v" || device != "d" || harness != "h" || content != "body" {
			t.Fatal("routing projection changed", key, version, device, harness)
		}
		var pending, dispatched bool
		var uncertain int
		query := "SELECT (" + target.Pending + ")," + target.Dispatched + "," + target.Uncertain + " FROM " + target.From
		if err = Row(context.Background(), db.db, query).Scan(&pending, &dispatched, &uncertain); err != nil || pending != test.pending || dispatched != test.dispatched || uncertain != test.uncertain {
			t.Fatal(pending, dispatched, uncertain, err)
		}
	}
}
