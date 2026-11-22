package database

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

const InitialMigration = "0001_initial"
const UpgradeMigration = "0002_server_upgrades"
const ClientVersionMigration = "0005_client_version"
const RunEventPageMigration = "0006_run_event_page"

func migrations() []*gormigrate.Migration {
	return append([]*gormigrate.Migration{{ID: InitialMigration, Migrate: func(db *gorm.DB) error {
		models := []any{&User{}, &initialDevice{}, &DeviceHarness{}, &DeviceCapability{}, &Conversation{}, &Message{}, &ConversationRound{}, &Agent{}, &AgentVersion{}, &AgentVersionSkill{}, &Skill{}, &SkillVersion{}, &Attachment{}, &initialRun{}, &RunInput{}, &RunEvent{}, &RunRequest{}, &RunControl{}, &TransactionLock{}}
		db = db.Session(&gorm.Session{NewDB: true})
		for _, model := range models {
			query := db.Session(&gorm.Session{NewDB: true})
			if db.Dialector.Name() == "mysql" {
				query = query.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin")
			}
			if err := query.Migrator().CreateTable(model); err != nil {
				return fmt.Errorf("migration %s: create %T: %w", InitialMigration, model, err)
			}
		}
		return db.Create(&TransactionLock{ID: 1}).Error
	}}, {ID: UpgradeMigration, Migrate: func(db *gorm.DB) error {
		if db.Dialector.Name() == "mysql" {
			db = db.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin")
		}
		return db.Migrator().CreateTable(&ServerUpgrade{})
	}}}, securityMigration(), deviceKeyMigration(), clientVersionMigration(), &gormigrate.Migration{
		ID: RunEventPageMigration,
		Migrate: func(db *gorm.DB) error {
			if db.Migrator().HasIndex(&RunEvent{}, "run_events_run_page") {
				return nil
			}
			return db.Exec("CREATE INDEX run_events_run_page ON run_events (conversation_id, run_id, id)").Error
		},
	})
}

func clientVersionMigration() *gormigrate.Migration {
	return &gormigrate.Migration{ID: ClientVersionMigration, Migrate: func(db *gorm.DB) error {
		if db.Migrator().HasColumn(&Device{}, "ClientVersion") {
			return nil
		}
		return db.Migrator().AddColumn(&Device{}, "ClientVersion")
	}}
}

func migrate(db *gorm.DB) error {
	// Pin a connection: session migration locks must be released on that same
	// connection. MySQL DDL commits implicitly, so a transaction is not a lock.
	return db.Connection(func(conn *gorm.DB) error {
		switch db.Dialector.Name() {
		case "postgres":
			if err := conn.Exec("SELECT pg_advisory_lock(71968237410)").Error; err != nil {
				return err
			}
			defer conn.Exec("SELECT pg_advisory_unlock(71968237410)")
		case "mysql":
			var name string
			if err := conn.Raw("SELECT DATABASE()").Row().Scan(&name); err != nil {
				return err
			}
			digest := sha256.Sum256([]byte(name))
			name = "agentuse_migrate_" + hex.EncodeToString(digest[:16])
			var locked int
			if err := conn.Raw("SELECT GET_LOCK(?, 60)", name).Row().Scan(&locked); err != nil {
				return err
			}
			if locked != 1 {
				return fmt.Errorf("timed out acquiring database migration lock")
			}
			defer conn.Exec("SELECT RELEASE_LOCK(?)", name)

		case "sqlite":
			return conn.Transaction(runMigrations)
		}
		if db.Dialector.Name() == "postgres" {
			return conn.Session(&gorm.Session{NewDB: true}).Transaction(runMigrations)
		}
		return runMigrations(conn)
	})
}

func runMigrations(db *gorm.DB) error {
	db = db.Session(&gorm.Session{NewDB: true})
	if !db.Migrator().HasTable("schema_migrations") {
		tables, err := db.Migrator().GetTables()
		if err != nil {
			return err
		}
		for _, table := range tables {
			if db.Dialector.Name() == "sqlite" && strings.HasPrefix(table, "sqlite_") {
				continue
			}
			return fmt.Errorf("unsupported database schema: reset the database before starting this version")
		}
	}
	options := *gormigrate.DefaultOptions
	options.TableName = "schema_migrations"
	options.ValidateUnknownMigrations = true
	// SQLite already runs inside its IMMEDIATE transaction. MySQL does not
	// promise transactional DDL; each completed step is recorded explicitly.
	return gormigrate.New(db, &options, migrations()).Migrate()
}
