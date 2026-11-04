package agentuse

type SkillRef struct {
	SkillID        string `json:"skill_id"`
	SkillVersionID string `json:"skill_version_id"`
}
type SkillVersion struct {
	ID           string `json:"id"`
	SkillID      string `json:"skill_id"`
	Version      int    `json:"version"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	PackagePath  string `json:"package_path,omitempty"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	DownloadPath string `json:"download_path"`
	CreatedAt    int64  `json:"created_at"`
}
type Skill struct {
	ID               string       `json:"id"`
	UserID           string       `json:"user_id"`
	CurrentVersionID string       `json:"current_version_id"`
	CurrentVersion   SkillVersion `json:"current_version"`
	CreatedAt        int64        `json:"created_at"`
	UpdatedAt        int64        `json:"updated_at"`
	DeletedAt        *int64       `json:"deleted_at,omitempty"`
}
type AgentConfig struct {
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	DaemonID     string     `json:"daemon_id"`
	Harness      string     `json:"harness"`
	Model        string     `json:"model,omitempty"`
	Instructions string     `json:"instructions"`
	Skills       []SkillRef `json:"skills"`
}
type AgentVersion struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id"`
	Version int    `json:"version"`
	AgentConfig
	CreatedAt int64 `json:"created_at"`
}
type PublishAgentVersionRequest struct {
	BaseVersionID string `json:"base_version_id"`
	AgentConfig
}
