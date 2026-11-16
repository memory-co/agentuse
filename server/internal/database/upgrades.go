package database

// ServerUpgrade is the frozen schema introduced by 0002_server_upgrades.
type ServerUpgrade struct {
	Seq             int64  `gorm:"primaryKey;autoIncrement;index:server_upgrades_instance,priority:2"`
	ID              string `gorm:"size:128;not null;uniqueIndex:server_upgrades_id"`
	InstanceKey     string `gorm:"size:64;not null;index:server_upgrades_instance,priority:1;uniqueIndex:server_upgrades_active,priority:1"`
	OwnerPID        int    `gorm:"column:owner_pid;not null"`
	ActiveMarker    *int16 `gorm:"uniqueIndex:server_upgrades_active,priority:2"`
	FromVersion     string `gorm:"size:128;not null"`
	TargetVersion   string `gorm:"size:128;not null;default:('')"`
	Status          string `gorm:"size:32;not null"`
	ReleaseURL      Text   `gorm:"not null;default:('')"`
	DownloadedBytes int64  `gorm:"not null;default:0"`
	TotalBytes      int64  `gorm:"not null;default:0"`
	Error           Text   `gorm:"not null;default:('')"`
	RequestedBy     string `gorm:"size:128;not null"`
	CreatedAt       int64  `gorm:"autoCreateTime:false;not null"`
	UpdatedAt       int64  `gorm:"autoUpdateTime:false;not null"`
	CompletedAt     *int64
}
