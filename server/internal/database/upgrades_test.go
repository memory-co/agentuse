package database

import (
	"context"
	"testing"
)

func TestUpgradeHistoryMigrationPreservesInitialDatabase(t *testing.T) {
	config := backendConfig(t)
	db, err := OpenConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.db.Migrator().DropTable(&ServerUpgrade{}); err != nil {
		t.Fatal(err)
	}
	if err = db.db.Exec("DELETE FROM schema_migrations WHERE id=?", UpgradeMigration).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.db.Create(&User{ID: "preserved", Name: "preserved", TokenHash: "hash", CreatedAt: 1}).Error; err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = OpenConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = Row(context.Background(), db.db, "SELECT count(*) FROM users WHERE id=?", "preserved").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if !db.db.Migrator().HasTable(&ServerUpgrade{}) {
		t.Fatal("upgrade history migration did not run")
	}
	if err = Row(context.Background(), db.db, "SELECT count(*) FROM schema_migrations WHERE id=?", UpgradeMigration).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
