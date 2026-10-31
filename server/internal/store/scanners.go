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
	var daemon, desc sql.NullString
	err := row.Scan(&a.ID, &a.UserID, &a.Name, &daemon, &a.Harness, &desc, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	a.DaemonID, a.Description = daemon.String, desc.String
	return a, err
}

func ScanAttachment(row scanner) (attachment.Attachment, error) {
	var a attachment.Attachment
	err := row.Scan(&a.ID, &a.SessionID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.SHA256, &a.CreatedBy, &a.Source, &a.DaemonID, &a.CreatedAt, &a.CreatedByRunID, &a.CreatedByAgentKey)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	a.DownloadPath = "/api/sessions/" + a.SessionID + "/attachments/" + a.ID + "/content"
	return a, err
}

func ScanDaemon(row scanner) (Daemon, error) {
	var d Daemon
	var token sql.NullString
	var seen sql.NullInt64
	err := row.Scan(&d.ID, &d.UserID, &d.Name, &token, &d.Status, &seen, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Daemon{}, ErrNotFound
	}
	d.SetupToken, d.LastSeenAt = token.String, nullInt(seen)
	d.Harnesses = []DaemonHarness{}
	d.Capabilities = []string{}
	return d, err
}

func ScanRun(row scanner) (Run, error) {
	var r Run
	err := row.Scan(&r.ID, &r.SessionID, &r.MessageID, &r.AgentID, &r.AgentKey, &r.AgentName, &r.AgentUserID, &r.DaemonID, &r.Harness, &r.Status, &r.AttemptID, &r.LeaseUntil, &r.ContextSeq, &r.ResultMessageID, &r.Error, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return r, err
}

func ScanSession(row scanner) (Session, error) {
	var ss Session
	var work sql.NullString
	err := row.Scan(&ss.ID, &ss.UserID, &ss.CreatedBy, &work, &ss.WorkRole, &ss.Title, &ss.Status, &ss.CreatedAt, &ss.UpdatedAt, &ss.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	ss.WorkID = work.String
	return ss, err
}

func ScanMessage(row scanner) (Message, error) {
	var m Message
	var content, targets string
	err := row.Scan(&m.ID, &m.SessionID, &m.Role, &content, &m.CreatedAt, &m.RunID, &m.AgentKey, &m.AgentName, &targets)
	m.Seq = m.ID
	_ = json.Unmarshal([]byte(targets), &m.TargetAgentIDs)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	m.Content = json.RawMessage(content)
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

func ScanWork(row scanner) (Work, error) {
	var w Work
	err := row.Scan(&w.ID, &w.UserID, &w.Name, &w.Instructions, &w.CoordinatorID, &w.ThreadCount, &w.CreatedAt, &w.UpdatedAt, &w.CoordinatorAgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return Work{}, ErrNotFound
	}
	return w, err
}
