package database

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/memory-co/agentuse/internal/attachment"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
)

func ScanAgent(row Scanner) (agentuse.Agent, error) {
	var a agentuse.Agent
	var device, desc sql.NullString
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &device, &a.Harness, &desc, &a.CreatedAt, &a.UpdatedAt, &a.CurrentVersionID, &a.Version, &a.Model, &a.Instructions)
	if errors.Is(err, sql.ErrNoRows) {
		return agentuse.Agent{}, ErrNotFound
	}
	a.DeviceID, a.Description = device.String, desc.String
	return a, err
}

func ScanAttachment(row Scanner) (attachment.Attachment, error) {
	var a attachment.Attachment
	err := row.Scan(&a.ID, &a.ConversationID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.CreatedBy, &a.Source, &a.DeviceID, &a.CreatedAt, &a.CreatedByRunID, &a.CreatedByAgentID, &a.RelativePath)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	a.DownloadPath = "/api/conversations/" + a.ConversationID + "/attachments/" + a.ID + "/content"
	return a, err
}

func ScanDevice(row Scanner) (agentuse.Device, error) {
	var d agentuse.Device
	var seen sql.NullInt64
	err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.Status, &seen, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return agentuse.Device{}, ErrNotFound
	}
	d.LastSeenAt = nullInt(seen)
	d.Harnesses = []agentuse.DeviceHarness{}
	d.Capabilities = []string{}
	return d, err
}

func ScanRun(row Scanner) (agentuse.Run, error) {
	var r agentuse.Run
	var result sql.NullString
	err := row.Scan(&r.ID, &r.ConversationID, &r.FirstMessageID, &r.AgentID, &r.AgentVersionID, &r.DeviceID, &r.SessionID, &r.Harness, &r.HarnessRevision, &r.Status, &r.ResultMessageID, &r.Output, &r.Error, &result, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	if result.Valid {
		r.Result = json.RawMessage(result.String)
		var facts struct {
			Model *string            `json:"model"`
			Usage *agentuse.RunUsage `json:"usage"`
		}
		if decodeErr := json.Unmarshal(r.Result, &facts); decodeErr != nil {
			return r, decodeErr
		}
		r.Model = facts.Model
		r.Usage = facts.Usage
	}
	return r, err
}

func ScanConversation(row Scanner) (agentuse.Conversation, error) {
	var ss agentuse.Conversation
	err := row.Scan(&ss.ID, &ss.UserID, &ss.CreatedBy, &ss.ParentConversationID, &ss.ParentAgentID, &ss.Mode, &ss.Title, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return agentuse.Conversation{}, ErrNotFound
	}
	return ss, err
}

func ScanMessage(row Scanner) (agentuse.Message, error) {
	var m agentuse.Message
	var content, agents, users, attachments, targets string
	err := row.Scan(&m.ID, &m.ConversationID, &m.AuthorRole, &content, &m.CreatedAt, &m.AuthorAgentID, &m.AuthorName, &agents, &users, &m.AuthorUserID, &m.ReplyToMessageID, &m.AuthorAgentVersionID, &m.Seq, &attachments, &m.AuthorConversationID, &m.AuthorRoundSeq, &targets)
	if errors.Is(err, sql.ErrNoRows) {
		return agentuse.Message{}, ErrNotFound
	}
	if err != nil {
		return agentuse.Message{}, err
	}
	if err = json.Unmarshal([]byte(agents), &m.ToAgentIDs); err != nil {
		return agentuse.Message{}, err
	}
	if err = json.Unmarshal([]byte(users), &m.ToUserIDs); err != nil {
		return agentuse.Message{}, err
	}
	if err = json.Unmarshal([]byte(attachments), &m.Attachments); err != nil {
		return agentuse.Message{}, err
	}
	var metadata map[string]struct {
		Error *agentuse.MessageError `json:"error"`
	}
	if err = json.Unmarshal([]byte(targets), &metadata); err != nil {
		return agentuse.Message{}, err
	}
	for agent, target := range metadata {
		if target.Error != nil {
			if m.AgentErrors == nil {
				m.AgentErrors = map[string]agentuse.MessageError{}
			}
			m.AgentErrors[agent] = *target.Error
		}
	}
	m.Content = content
	return m, err
}

func ScanUser(row Scanner) (agentuse.User, error) {
	var u agentuse.User
	err := row.Scan(&u.ID, &u.Name, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return agentuse.User{}, ErrNotFound
	}
	return u, err
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
