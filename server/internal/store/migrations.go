package store

import (
	"fmt"
	"strings"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

const InitialMigration = "0001_initial"

func migrations() []*gormigrate.Migration {
	return []*gormigrate.Migration{{ID: InitialMigration, Migrate: func(db *gorm.DB) error {
		models := []any{&User{}, &Device{}, &DeviceHarness{}, &DeviceCapability{}, &Conversation{}, &Message{}, &ConversationRound{}, &Agent{}, &AgentVersion{}, &AgentVersionSkill{}, &Skill{}, &SkillVersion{}, &Attachment{}, &Run{}, &RunInput{}, &RunEvent{}, &RunRequest{}, &RunControl{}, &StoreLock{}}
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
		return db.Create(&StoreLock{ID: 1}).Error
	}}}
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
			name = "agentuse_migrate_" + HashSecret(name)[:32]
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
