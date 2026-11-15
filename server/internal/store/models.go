package store

import (
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// JSON and Text describe storage types, independently of the public SDK models.
type JSON string

func (JSON) GormDataType() string { return "json" }
func (JSON) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	switch db.Dialector.Name() {
	case "postgres":
		return "jsonb"
	case "mysql":
		return "json"
	default:
		return "text"
	}
}

type Text string

func (Text) GormDataType() string { return "text" }
func (Text) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "longtext"
	}
	return "text"
}

// These definitions are the frozen baseline for migration 0001_initial.
// Later schema changes belong in new migrations, never in this baseline.
type User struct {
	ID        string `gorm:"size:128;primaryKey"`
	Name      string `gorm:"size:191;not null;uniqueIndex:users_name"`
	Role      string `gorm:"size:16;not null;default:user"`
	TokenHash string `gorm:"size:64;not null;uniqueIndex:users_token"`
	CreatedAt int64  `gorm:"autoCreateTime:false;not null"`
}

type Device struct {
	ID         string  `gorm:"size:128;primaryKey"`
	UserID     string  `gorm:"size:128;not null"`
	Name       Text    `gorm:"not null"`
	KeyHash    *string `gorm:"size:64;uniqueIndex:devices_key"`
	SetupToken *string `gorm:"size:64;uniqueIndex:devices_setup_token"`
	Status     string  `gorm:"size:16;not null;default:pending"`
	LastSeenAt *int64
	CreatedAt  int64 `gorm:"autoCreateTime:false;not null"`
}

type DeviceHarness struct {
	DeviceID        string  `gorm:"size:128;primaryKey"`
	Harness         string  `gorm:"size:128;primaryKey;index:device_harnesses_user_harness,priority:2"`
	UserID          string  `gorm:"size:128;not null;index:device_harnesses_user_harness,priority:1"`
	Version         *string `gorm:"size:512"`
	HarnessRevision Text    `gorm:"not null;default:('')"`
	Capabilities    JSON    `gorm:"not null;default:('[]')"`
	Available       int16   `gorm:"not null;default:0"`
	Enabled         int16   `gorm:"not null;default:0"`
	UpdatedAt       int64   `gorm:"autoUpdateTime:false;not null"`
}

type DeviceCapability struct {
	DeviceID   string `gorm:"size:128;primaryKey"`
	Capability string `gorm:"size:128;primaryKey"`
}

type Conversation struct {
	ID                   string  `gorm:"size:128;primaryKey;index:conversations_parent,priority:3"`
	UserID               string  `gorm:"size:128;not null;index:conversations_user,priority:1"`
	CreatedBy            string  `gorm:"size:128;not null;index:conversations_created_by,priority:1"`
	ParentConversationID *string `gorm:"size:128;index:conversations_parent,priority:1;check:conversations_parent_mode,parent_conversation_id IS NULL OR mode='single'"`
	ParentAgentID        *string `gorm:"size:128;check:conversations_parent_identity,(parent_conversation_id IS NULL AND parent_agent_id IS NULL) OR (parent_conversation_id IS NOT NULL AND parent_agent_id IS NOT NULL)"`
	Mode                 string  `gorm:"size:16;not null;default:single;check:conversations_mode,mode IN ('single','collaborative')"`
	Title                Text    `gorm:"not null;default:('')"`
	Status               string  `gorm:"size:16;not null;default:idle;index:conversations_status,priority:1"`
	CreatedAt            int64   `gorm:"autoCreateTime:false;not null"`
	UpdatedAt            int64   `gorm:"autoUpdateTime:false;not null;index:conversations_parent,priority:2;index:conversations_status,priority:2;index:conversations_user,priority:2;index:conversations_created_by,priority:2"`
}

type Message struct {
	ID                   string  `gorm:"size:128;primaryKey"`
	Seq                  int64   `gorm:"not null;uniqueIndex:messages_conversation,priority:2;index:messages_replies,priority:3"`
	ConversationID       string  `gorm:"size:128;not null;uniqueIndex:messages_conversation,priority:1;index:messages_replies,priority:1;uniqueIndex:messages_child_round,priority:1"`
	AuthorRole           string  `gorm:"size:32;not null;check:messages_role,author_role IN ('user','agent','child_conversation')"`
	Content              Text    `gorm:"not null"`
	Attachments          JSON    `gorm:"not null;default:('[]')"`
	AuthorUserID         *string `gorm:"size:128"`
	AuthorAgentID        *string `gorm:"size:128"`
	ToAgentIDs           JSON    `gorm:"not null;default:('[]')"`
	ToUserIDs            JSON    `gorm:"not null;default:('[]')"`
	ReplyToMessageID     *string `gorm:"size:128;index:messages_replies,priority:2"`
	AuthorAgentVersionID *string `gorm:"size:128"`
	AuthorConversationID *string `gorm:"size:128;uniqueIndex:messages_child_round,priority:2"`
	AuthorRoundSeq       *int64  `gorm:"uniqueIndex:messages_child_round,priority:3;check:messages_author_identity,(author_role='child_conversation' AND author_conversation_id IS NOT NULL AND author_round_seq IS NOT NULL AND author_round_seq>0 AND author_user_id IS NULL AND author_agent_id IS NULL AND author_agent_version_id IS NULL) OR (author_role IN ('user','agent') AND author_conversation_id IS NULL AND author_round_seq IS NULL)"`
	AgentTargets         JSON    `gorm:"not null;default:('{}')"`
	CreatedAt            int64   `gorm:"autoCreateTime:false;not null"`
}

type ConversationRound struct {
	ConversationID  string `gorm:"size:128;primaryKey;uniqueIndex:conversation_rounds_active,priority:1"`
	Seq             int64  `gorm:"primaryKey;autoIncrement:false;check:conversation_rounds_seq,seq>0"`
	StartMessageSeq int64  `gorm:"not null;check:conversation_rounds_start,start_message_seq>0"`
	EndMessageSeq   *int64
	CreatedAt       int64  `gorm:"autoCreateTime:false;not null"`
	FinishedAt      *int64 `gorm:"check:conversation_rounds_finish,(end_message_seq IS NULL AND finished_at IS NULL) OR (end_message_seq IS NOT NULL AND end_message_seq>=start_message_seq AND finished_at IS NOT NULL)"`
	// NULL values do not collide in a unique index on any supported database.
	ActiveMarker *int16 `gorm:"type:smallint GENERATED ALWAYS AS (CASE WHEN finished_at IS NULL THEN 1 ELSE NULL END) STORED;->;uniqueIndex:conversation_rounds_active,priority:2"`
}

type Agent struct {
	ID               string `gorm:"size:128;primaryKey"`
	UserID           string `gorm:"size:128;not null;index:agents_user,priority:1"`
	CurrentVersionID string `gorm:"size:128;not null"`
	CreatedAt        int64  `gorm:"autoCreateTime:false;not null"`
	UpdatedAt        int64  `gorm:"autoUpdateTime:false;not null;index:agents_user,priority:2"`
	DeletedAt        *int64
}

type AgentVersion struct {
	ID           string `gorm:"size:128;primaryKey"`
	AgentID      string `gorm:"size:128;not null;uniqueIndex:agent_versions_agent,priority:1"`
	Version      int64  `gorm:"not null;uniqueIndex:agent_versions_agent,priority:2;check:agent_versions_positive,version>0"`
	Name         Text   `gorm:"not null"`
	Description  Text   `gorm:"not null;default:('')"`
	DeviceID     string `gorm:"size:128;not null"`
	Harness      string `gorm:"size:128;not null"`
	Model        *Text
	Instructions Text  `gorm:"not null;default:('')"`
	CreatedAt    int64 `gorm:"autoCreateTime:false;not null"`
}

type AgentVersionSkill struct {
	AgentVersionID string `gorm:"size:128;primaryKey;uniqueIndex:agent_version_skills_position,priority:1"`
	SkillID        string `gorm:"size:128;primaryKey"`
	SkillVersionID string `gorm:"size:128;not null;index:agent_version_skills_version"`
	Position       int64  `gorm:"not null;uniqueIndex:agent_version_skills_position,priority:2;check:agent_version_skills_position_valid,position>=0"`
}

type Skill struct {
	ID               string `gorm:"size:128;primaryKey"`
	UserID           string `gorm:"size:128;not null;index:skills_user,priority:1"`
	CurrentVersionID string `gorm:"size:128;not null"`
	CreatedAt        int64  `gorm:"autoCreateTime:false;not null"`
	UpdatedAt        int64  `gorm:"autoUpdateTime:false;not null;index:skills_user,priority:2"`
	DeletedAt        *int64
}

type SkillVersion struct {
	ID          string `gorm:"size:128;primaryKey"`
	SkillID     string `gorm:"size:128;not null;uniqueIndex:skill_versions_skill,priority:1"`
	Version     int64  `gorm:"not null;uniqueIndex:skill_versions_skill,priority:2;check:skill_versions_positive,version>0"`
	Name        Text   `gorm:"not null"`
	Description Text   `gorm:"not null"`
	PackagePath string `gorm:"size:512;not null;uniqueIndex:skill_versions_package"`
	SHA256      string `gorm:"column:sha256;size:64;not null"`
	SizeBytes   int64  `gorm:"not null;check:skill_versions_size,size_bytes>0"`
	CreatedAt   int64  `gorm:"autoCreateTime:false;not null"`
}

type Attachment struct {
	ID             string `gorm:"size:128;primaryKey;index:attachments_conversation,priority:3"`
	ConversationID string `gorm:"size:128;not null;uniqueIndex:attachments_path,priority:1;index:attachments_conversation,priority:1"`
	RelativePath   Text   `gorm:"not null"`
	// Index the full canonical path without MySQL's indexed-string length limit.
	PathHash       string  `gorm:"size:64;not null;uniqueIndex:attachments_path,priority:2"`
	Filename       string  `gorm:"size:255;not null"`
	ContentType    string  `gorm:"size:255;not null"`
	SizeBytes      int64   `gorm:"not null;check:attachments_size,size_bytes>=0"`
	SHA256         string  `gorm:"column:sha256;size:64;not null;check:attachments_digest,length(sha256)=64"`
	CreatedBy      string  `gorm:"size:128;not null"`
	Source         string  `gorm:"size:16;not null;check:attachments_source,source IN ('user','device')"`
	DeviceID       *string `gorm:"size:128"`
	CreatedByRunID *string `gorm:"size:128"`
	CreatedAt      int64   `gorm:"autoCreateTime:false;not null;index:attachments_conversation,priority:2"`
}

type Run struct {
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

type RunInput struct {
	RunID     string `gorm:"size:128;primaryKey;uniqueIndex:run_inputs_message,priority:1"`
	InputID   string `gorm:"size:128;primaryKey"`
	MessageID string `gorm:"size:128;not null;uniqueIndex:run_inputs_message,priority:2;index:run_inputs_source"`
	CreatedAt int64  `gorm:"autoCreateTime:false;not null"`
}

type RunEvent struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	ConversationID string `gorm:"size:128;not null"`
	SessionID      string `gorm:"size:128;not null;uniqueIndex:run_events_source,priority:2"`
	RunID          string `gorm:"size:128;not null;default:''"`
	AgentID        string `gorm:"size:128;not null"`
	AgentVersionID string `gorm:"size:128;not null"`
	DeviceID       string `gorm:"size:128;not null;uniqueIndex:run_events_source,priority:1"`
	EventSeq       int64  `gorm:"not null;uniqueIndex:run_events_source,priority:3"`
	MessageID      string `gorm:"size:128;not null;default:''"`
	Kind           string `gorm:"size:32;not null"`
	Payload        Text   `gorm:"not null"`
	CreatedAt      int64  `gorm:"autoCreateTime:false;not null"`
}

type RunRequest struct {
	RequestID      string `gorm:"size:128;primaryKey"`
	ConversationID string `gorm:"size:128;not null"`
	RunID          string `gorm:"size:128;not null"`
	SessionID      string `gorm:"size:128;not null"`
	DeviceID       string `gorm:"size:128;not null"`
	Payload        Text   `gorm:"not null"`
	State          string `gorm:"size:16;not null"`
	Answer         *Text
	AnsweredBy     *string `gorm:"size:128"`
	CreatedAt      int64   `gorm:"autoCreateTime:false;not null"`
	UpdatedAt      int64   `gorm:"autoUpdateTime:false;not null"`
}

type RunControl struct {
	Seq            int64  `gorm:"primaryKey;autoIncrement"`
	ID             string `gorm:"size:128;not null;uniqueIndex:run_controls_id"`
	ConversationID string `gorm:"size:128;not null"`
	SessionID      string `gorm:"size:128;not null"`
	RunID          string `gorm:"size:128;not null"`
	DeviceID       string `gorm:"size:128;not null"`
	Kind           string `gorm:"size:32;not null"`
	RequestID      string `gorm:"size:128;not null;default:''"`
	Answer         *Text
	State          string `gorm:"size:16;not null;default:pending"`
	Error          Text   `gorm:"not null;default:('')"`
	CreatedAt      int64  `gorm:"autoCreateTime:false;not null"`
	UpdatedAt      int64  `gorm:"autoUpdateTime:false;not null"`
}

// A transaction-scoped row lock preserves the existing serialized metadata
// transaction semantics across all databases and across Server processes.
type StoreLock struct {
	ID int `gorm:"primaryKey;autoIncrement:false"`
}
