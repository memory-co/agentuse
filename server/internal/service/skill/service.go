// Package skill owns shared skills and immutable ZIP versions.
package skill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/skillbundle"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/service/file"
	"github.com/memory-co/agentuse/server/internal/store"
)

var ErrConflict = errors.New("version changed; reload before publishing")

type Service struct {
	DB    *sql.DB
	Files *file.Service
	Now   func() time.Time
}
type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const columns = `SELECT v.id,v.skill_id,v.version,v.name,v.description,v.package_path,v.sha256,v.size_bytes,v.created_at FROM skill_versions v`

func scan(row interface{ Scan(...any) error }) (agentuse.SkillVersion, error) {
	var v agentuse.SkillVersion
	err := row.Scan(&v.ID, &v.SkillID, &v.Version, &v.Name, &v.Description, &v.PackagePath, &v.SHA256, &v.SizeBytes, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = store.ErrNotFound
	}
	v.DownloadPath = "/api/skills/" + v.SkillID + "/versions/" + v.ID + "/package"
	return v, err
}
func (s *Service) Version(ctx context.Context, q Querier, user agentuse.User, id, version string, forPublish bool) (agentuse.SkillVersion, error) {
	var owner string
	var deleted sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT user_id,deleted_at FROM skills WHERE id=?`, id).Scan(&owner, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		err = store.ErrNotFound
	}
	if err != nil {
		return agentuse.SkillVersion{}, err
	}
	if (!user.IsAdmin() && owner != user.ID) || (forPublish && deleted.Valid) {
		return agentuse.SkillVersion{}, store.ErrNotFound
	}
	return scan(q.QueryRowContext(ctx, columns+` WHERE v.skill_id=? AND v.id=?`, id, version))
}
func (s *Service) Publish(ctx context.Context, user agentuse.User, id, base string, source io.Reader) (agentuse.Skill, error) {
	var out agentuse.Skill
	if s.Files == nil {
		return out, fmt.Errorf("file storage is not configured")
	}
	data, err := io.ReadAll(io.LimitReader(source, skillbundle.MaxArchive+1))
	if err != nil {
		return out, err
	}
	meta, err := skillbundle.Inspect(data)
	if err != nil {
		return out, err
	}
	fresh := id == ""
	if fresh {
		id = uuid.NewString()
	}
	versionID := uuid.NewString()
	key := id + "/" + versionID + "/skill.zip"
	if !fresh {
		var owner, head string
		var deleted sql.NullInt64
		err = s.DB.QueryRowContext(ctx, `SELECT user_id,current_version_id,deleted_at FROM skills WHERE id=?`, id).Scan(&owner, &head, &deleted)
		if err != nil || deleted.Valid || (!user.IsAdmin() && owner != user.ID) {
			return out, store.ErrNotFound
		}
		if base == "" || head != base {
			return out, ErrConflict
		}
	}
	if err = s.Files.Put(ctx, "skills", key, bytes.NewReader(data), true); err != nil {
		return out, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.Files.Delete(context.Background(), "skills", key)
		}
	}()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	owner := user.ID
	version := 1
	created := s.Now().Unix()
	now := created
	if !fresh {
		var head string
		var deleted sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT user_id,current_version_id,created_at,deleted_at FROM skills WHERE id=?`, id).Scan(&owner, &head, &created, &deleted)
		if err != nil || deleted.Valid || (!user.IsAdmin() && owner != user.ID) {
			return out, store.ErrNotFound
		}
		if head != base {
			return out, ErrConflict
		}
		if err = tx.QueryRowContext(ctx, `SELECT version+1 FROM skill_versions WHERE id=? AND skill_id=?`, head, id).Scan(&version); err != nil {
			return out, err
		}
	}
	digest := sha256.Sum256(data)
	v := agentuse.SkillVersion{ID: versionID, SkillID: id, Version: version, Name: meta.Name, Description: meta.Description, PackagePath: key, SHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(data)), CreatedAt: now, DownloadPath: "/api/skills/" + id + "/versions/" + versionID + "/package"}
	if _, err = tx.ExecContext(ctx, `INSERT INTO skill_versions(id,skill_id,version,name,description,package_path,sha256,size_bytes,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, v.ID, id, version, v.Name, v.Description, key, v.SHA256, v.SizeBytes, now); err != nil {
		return out, err
	}
	if fresh {
		_, err = tx.ExecContext(ctx, `INSERT INTO skills(id,user_id,current_version_id,created_at,updated_at) VALUES(?,?,?,?,?)`, id, owner, versionID, now, now)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE skills SET current_version_id=?,updated_at=? WHERE id=?`, versionID, now, id)
	}
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	committed = true
	return agentuse.Skill{ID: id, UserID: owner, CurrentVersionID: versionID, CurrentVersion: v, CreatedAt: created, UpdatedAt: now}, nil
}
func (s *Service) Get(ctx context.Context, user agentuse.User, id string) (agentuse.Skill, error) {
	var out agentuse.Skill
	err := s.DB.QueryRowContext(ctx, `SELECT id,user_id,current_version_id,created_at,updated_at,deleted_at FROM skills WHERE id=? AND deleted_at IS NULL`, id).Scan(&out.ID, &out.UserID, &out.CurrentVersionID, &out.CreatedAt, &out.UpdatedAt, &out.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = store.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if !user.IsAdmin() && out.UserID != user.ID {
		return out, store.ErrNotFound
	}
	out.CurrentVersion, err = s.Version(ctx, s.DB, user, id, out.CurrentVersionID, false)
	return out, err
}
func (s *Service) List(ctx context.Context, user agentuse.User) ([]agentuse.Skill, error) {
	query := `SELECT id FROM skills WHERE deleted_at IS NULL`
	args := []any{}
	if !user.IsAdmin() {
		query += ` AND user_id=?`
		args = append(args, user.ID)
	}
	query += ` ORDER BY updated_at DESC,id`
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []agentuse.Skill{}
	for _, id := range ids {
		v, e := s.Get(ctx, user, id)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *Service) Versions(ctx context.Context, user agentuse.User, id string) ([]agentuse.SkillVersion, error) {
	if _, err := s.Get(ctx, user, id); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, columns+` WHERE v.skill_id=? ORDER BY v.version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentuse.SkillVersion{}
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Delete(ctx context.Context, user agentuse.User, id string) error {
	if _, err := s.Get(ctx, user, id); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE skills SET deleted_at=? WHERE id=?`, s.Now().Unix(), id)
	return err
}
