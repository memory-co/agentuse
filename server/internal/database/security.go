package database

import (
	"fmt"
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// Initial snapshots freeze the published baseline before security columns existed.
type initialDevice struct {
	ID         string  `gorm:"size:128;primaryKey"`
	UserID     string  `gorm:"size:128;not null"`
	Name       Text    `gorm:"not null"`
	KeyHash    *string `gorm:"size:64;uniqueIndex:devices_key"`
	SetupToken *string `gorm:"size:64;uniqueIndex:devices_setup_token"`
	Status     string  `gorm:"size:16;not null;default:pending"`
	LastSeenAt *int64
	CreatedAt  int64 `gorm:"autoCreateTime:false;not null"`
}

func (initialDevice) TableName() string { return "devices" }

type initialRun struct {
	ID              string  `gorm:"size:128;primaryKey;index:runs_conversation,priority:3"`
	ConversationID  string  `gorm:"size:128;not null;index:runs_conversation,priority:1"`
	FirstMessageID  string  `gorm:"size:128;not null"`
	AgentID         string  `gorm:"size:128;not null"`
	AgentVersionID  string  `gorm:"size:128;not null"`
	DeviceID        string  `gorm:"size:128;not null"`
	SessionID       string  `gorm:"size:128;not null"`
	Harness         string  `gorm:"size:128;not null"`
	HarnessRevision Text    `gorm:"not null"`
	Status          string  `gorm:"size:16;not null;default:running"`
	ResultMessageID *string `gorm:"size:128"`
	Output          Text    `gorm:"not null;default:('')"`
	Error           Text    `gorm:"not null;default:('')"`
	Result          *Text
	CreatedAt       int64 `gorm:"autoCreateTime:false;not null;index:runs_conversation,priority:2"`
	UpdatedAt       int64 `gorm:"autoUpdateTime:false;not null"`
}

func (initialRun) TableName() string { return "runs" }

type DeviceCertificate struct {
	Serial     string `gorm:"size:64;primaryKey"`
	DeviceID   string `gorm:"size:128;not null;index:device_certificates_device"`
	Generation int64  `gorm:"not null"`
	KeyHash    string `gorm:"size:64;not null"`
	NotBefore  int64  `gorm:"not null"`
	NotAfter   int64  `gorm:"not null"`
	RevokedAt  *int64
}

const SecurityMigration = "0003_device_security"

func securityMigration() *gormigrate.Migration {
	return &gormigrate.Migration{ID: SecurityMigration, Migrate: func(db *gorm.DB) error {
		for _, v := range []struct {
			model any
			field string
		}{{&Device{}, "CredentialGeneration"}, {&Run{}, "AccessTokenHash"}, {&Run{}, "AccessExpiresAt"}} {
			if !db.Migrator().HasColumn(v.model, v.field) {
				if err := db.Migrator().AddColumn(v.model, v.field); err != nil {
					return fmt.Errorf("security column %s: %w", v.field, err)
				}
			}
		}
		if !db.Migrator().HasIndex(&Run{}, "runs_access_token") {
			if err := db.Migrator().CreateIndex(&Run{}, "runs_access_token"); err != nil {
				return err
			}
		}
		if !db.Migrator().HasTable(&DeviceCertificate{}) {
			if db.Dialector.Name() == "mysql" {
				db = db.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin")
			}
			return db.Migrator().CreateTable(&DeviceCertificate{})
		}
		return nil
	}}
}
