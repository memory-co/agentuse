package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/memory-co/agentuse/internal/attachment"
)

type scanner interface{ Scan(...any) error }

func ScanAgent(row scanner) (Agent, error) {
	var a Agent
	var device, desc sql.NullString
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &device, &a.Harness, &desc, &a.CreatedAt, &a.UpdatedAt, &a.CurrentVersionID, &a.Version, &a.Model, &a.Instructions)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	a.DeviceID, a.Description = device.String, desc.String
	return a, err
}

func ScanAttachment(row scanner) (attachment.Attachment, error) {
	var a attachment.Attachment
	err := row.Scan(&a.ID, &a.ConversationID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.CreatedBy, &a.Source, &a.DeviceID, &a.CreatedAt, &a.CreatedByRunID, &a.CreatedByAgentID, &a.RelativePath)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	a.DownloadPath = "/api/conversations/" + a.ConversationID + "/attachments/" + a.ID + "/content"
	return a, err
}

func ScanDevice(row scanner) (Device, error) {
	var d Device
	var token sql.NullString
	var seen sql.NullInt64
	err := row.Scan(&d.ID, &d.UserID, &d.Name, &token, &d.Status, &seen, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	d.SetupToken, d.LastSeenAt = token.String, nullInt(seen)
	d.Harnesses = []DeviceHarness{}
	d.Capabilities = []string{}
	return d, err
}

func ScanRun(row scanner) (Run, error) {
	var r Run
	var result sql.NullString
	err := row.Scan(&r.ID, &r.ConversationID, &r.FirstMessageID, &r.AgentID, &r.AgentVersionID, &r.DeviceID, &r.SessionID, &r.Harness, &r.HarnessRevision, &r.Status, &r.ResultMessageID, &r.Output, &r.Error, &result, &r.CreatedAt, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	if result.Valid {
		r.Result = json.RawMessage(result.String)
	}
	return r, err
}

func ScanConversation(row scanner) (Conversation, error) {
	var ss Conversation
	err := row.Scan(&ss.ID, &ss.UserID, &ss.CreatedBy, &ss.ParentConversationID, &ss.ParentAgentID, &ss.Mode, &ss.Title, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	return ss, err
}

func ScanMessage(row scanner) (Message, error) {
	var m Message
	var content, agents, users, attachments string
	err := row.Scan(&m.ID, &m.ConversationID, &m.AuthorRole, &content, &m.CreatedAt, &m.AuthorAgentID, &m.AuthorName, &agents, &users, &m.AuthorUserID, &m.ReplyToMessageID, &m.AuthorAgentVersionID, &m.Seq, &attachments, &m.AuthorConversationID, &m.AuthorRoundSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, err
	}
	if err = json.Unmarshal([]byte(agents), &m.ToAgentIDs); err != nil {
		return Message{}, err
	}
	if err = json.Unmarshal([]byte(users), &m.ToUserIDs); err != nil {
		return Message{}, err
	}
	if err = json.Unmarshal([]byte(attachments), &m.Attachments); err != nil {
		return Message{}, err
	}
	m.Content = content
	return m, err
}

func ScanUser(row scanner) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}
