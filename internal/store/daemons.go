package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

const (
	DaemonPending = "pending" // 已在页面上添加，还没跑 setup.sh 注册
	DaemonOnline  = "online"
	DaemonOffline = "offline"
)

type Daemon struct {
	Capabilities []string        `json:"capabilities"`
	ID           string          `json:"id"`
	UserID       string          `json:"user_id"`
	Name         string          `json:"name"`
	SetupToken   string          `json:"setup_token,omitempty"` // 注册前有值，注册后清空
	Status       string          `json:"status"`
	LastSeenAt   *int64          `json:"last_seen_at"`
	CreatedAt    int64           `json:"created_at"`
	Harnesses    []DaemonHarness `json:"harnesses"`
}

type DaemonHarness struct {
	DaemonID  string `json:"daemon_id"`
	UserID    string `json:"user_id"`
	Harness   string `json:"harness"`
	Version   string `json:"version"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt int64  `json:"updated_at"`
}

// ReportedHarness 是 daemon 连上时上报的一个本机可用 harness。
type ReportedHarness struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// CreateDaemon 在页面上添加一台 daemon:生成一次性的安装令牌，等用户在电脑上跑 setup.sh 注册。
func (s *Store) CreateDaemon(ctx context.Context, userID, name string) (Daemon, error) {
	d := Daemon{
		ID:         randomID("dm_"),
		UserID:     userID,
		Name:       strings.TrimSpace(name),
		SetupToken: RandomToken("st_"),
		Status:     DaemonPending,
		CreatedAt:  s.unix(),
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO daemons (id, user_id, name, setup_token, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.UserID, d.Name, d.SetupToken, d.Status, d.CreatedAt)
	return d, err
}

// RegisterDaemon 用安装令牌换长期密钥。令牌用一次就作废。
func (s *Store) RegisterDaemon(ctx context.Context, setupToken, hostname string) (Daemon, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Daemon{}, "", err
	}
	defer tx.Rollback()
	var d Daemon
	err = tx.QueryRowContext(ctx, `SELECT id, user_id, name FROM daemons WHERE setup_token = ?`, setupToken).
		Scan(&d.ID, &d.UserID, &d.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Daemon{}, "", ErrNotFound
	}
	if err != nil {
		return Daemon{}, "", err
	}
	if d.Name == "" {
		d.Name = strings.TrimSpace(hostname)
	}
	if d.Name == "" {
		d.Name = d.ID
	}
	key := RandomToken("dk_")
	if _, err := tx.ExecContext(ctx,
		`UPDATE daemons SET name = ?, key_hash = ?, setup_token = NULL, status = ? WHERE id = ?`,
		d.Name, HashSecret(key), DaemonOffline, d.ID); err != nil {
		return Daemon{}, "", err
	}
	d.Status = DaemonOffline
	return d, key, tx.Commit()
}

// ResetDaemonSetup 重新生成安装令牌(用于换电脑 / 重装),旧密钥立即失效。
func (s *Store) ResetDaemonSetup(ctx context.Context, u User, id string) (Daemon, error) {
	token := RandomToken("st_")
	q := `UPDATE daemons SET setup_token = ?, key_hash = NULL, status = ? WHERE id = ?`
	args := []any{token, DaemonPending, id}
	if !u.IsAdmin() {
		q += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return Daemon{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Daemon{}, ErrNotFound
	}
	return s.GetDaemon(ctx, u, id)
}

func (s *Store) DaemonByKey(ctx context.Context, key string) (Daemon, error) {
	return s.scanDaemon(s.db.QueryRowContext(ctx, daemonCols+` WHERE key_hash = ?`, HashSecret(key)))
}

const daemonCols = `SELECT id, user_id, name, setup_token, status, last_seen_at, created_at FROM daemons`

func (s *Store) scanDaemon(row scanner) (Daemon, error) {
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

// GetDaemon 取一台 daemon 及其 harness 列表;非 admin 只能看自己的。
func (s *Store) GetDaemon(ctx context.Context, u User, id string) (Daemon, error) {
	q := daemonCols + ` WHERE id = ?`
	args := []any{id}
	if !u.IsAdmin() {
		q += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	d, err := s.scanDaemon(s.db.QueryRowContext(ctx, q, args...))
	if err != nil {
		return d, err
	}
	harnesses, err := s.daemonHarnesses(ctx, `WHERE daemon_id = ?`, d.ID)
	d.Harnesses = harnesses
	if err == nil {
		d.Capabilities, err = s.daemonCapabilities(ctx, d.ID)
	}
	return d, err
}

// ListDaemons 列出用户的 daemon;admin 看所有人的。
func (s *Store) ListDaemons(ctx context.Context, u User) ([]Daemon, error) {
	q := daemonCols
	var args []any
	if !u.IsAdmin() {
		q += ` WHERE user_id = ?`
		args = append(args, u.ID)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at`, args...)
	if err != nil {
		return nil, err
	}
	out := []Daemon{}
	byID := map[string]int{}
	for rows.Next() {
		d, err := s.scanDaemon(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byID[d.ID] = len(out)
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	where, aargs := "", []any{}
	if !u.IsAdmin() {
		where, aargs = `WHERE user_id = ?`, []any{u.ID}
	}
	harnesses, err := s.daemonHarnesses(ctx, where, aargs...)
	if err != nil {
		return nil, err
	}
	for _, a := range harnesses {
		if i, ok := byID[a.DaemonID]; ok {
			out[i].Harnesses = append(out[i].Harnesses, a)
		}
	}
	for i := range out {
		caps, e := s.daemonCapabilities(ctx, out[i].ID)
		if e != nil {
			return nil, e
		}
		out[i].Capabilities = caps
	}
	return out, nil
}

func (s *Store) daemonHarnesses(ctx context.Context, where string, args ...any) ([]DaemonHarness, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT daemon_id, user_id, harness, version, available, enabled, updated_at FROM daemon_harnesses `+where+` ORDER BY harness`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DaemonHarness{}
	for rows.Next() {
		var a DaemonHarness
		var ver sql.NullString
		if err := rows.Scan(&a.DaemonID, &a.UserID, &a.Harness, &ver, &a.Available, &a.Enabled, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Version = ver.String
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteDaemon 删除 daemon;它名下没回完的会话回到 idle,记一条失败的回复。
func (s *Store) DeleteDaemon(ctx context.Context, u User, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := `DELETE FROM daemons WHERE id = ?`
	args := []any{id}
	if !u.IsAdmin() {
		q += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	var one int
	check := strings.Replace(q, "DELETE FROM", "SELECT 1 FROM", 1)
	if err := tx.QueryRowContext(ctx, check, args...).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := s.failDaemonSessions(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// DaemonConnected 处理 daemon 连上:标在线，按上报更新 daemon_harnesses。
// 上报里有的标 available=1 并保留原来的 enabled;没上报的标 available=0。
// 不在 harnesses 表里登记过的名字被忽略，返回给调用方。
func (s *Store) DaemonConnected(ctx context.Context, d Daemon, reported []ReportedHarness, caps ...[]string) (ignored []string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM daemon_capabilities WHERE daemon_id=?`, d.ID); err != nil {
		return nil, err
	}
	if len(caps) > 0 {
		for _, c := range caps[0] {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO daemon_capabilities VALUES(?,?)`, d.ID, c); err != nil {
				return nil, err
			}
		}
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx, `UPDATE daemons SET status = ?, last_seen_at = ? WHERE id = ?`, DaemonOnline, now, d.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE daemon_harnesses SET available = 0, updated_at = ? WHERE daemon_id = ?`, now, d.ID); err != nil {
		return nil, err
	}
	for _, a := range reported {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM harnesses WHERE name = ?`, a.Name).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			ignored = append(ignored, a.Name)
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO daemon_harnesses (daemon_id, user_id, harness, version, available, enabled, updated_at) VALUES (?, ?, ?, ?, 1, 0, ?)
			 ON CONFLICT(daemon_id, harness) DO UPDATE SET version = excluded.version, available = 1, updated_at = excluded.updated_at`,
			d.ID, d.UserID, a.Name, a.Version, now); err != nil {
			return nil, err
		}
	}
	// 这次没上报的 harness 上排着的会话派不出去了，直接判失败
	if err := s.failUnrunnable(ctx, tx, d.ID); err != nil {
		return nil, err
	}
	return ignored, tx.Commit()
}

func (s *Store) DaemonDisconnected(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE daemons SET status = ?, last_seen_at = ? WHERE id = ? AND status = ?`,
		DaemonOffline, s.unix(), id, DaemonOnline)
	return err
}

func (s *Store) TouchDaemon(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE daemons SET last_seen_at = ? WHERE id = ?`, s.unix(), id)
	return err
}

// MarkAllDaemonsOffline 在服务启动时调用:上次进程留下的 online 都不作数了。
func (s *Store) MarkAllDaemonsOffline(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE daemons SET status = ? WHERE status = ?`, DaemonOffline, DaemonOnline)
	return err
}

// SetDaemonHarnessEnabled 开关某台 daemon 上的某个 harness。只能开本机上报可用的。
// 关掉时，这个 harness 上排队中的会话直接判失败。
func (s *Store) SetDaemonHarnessEnabled(ctx context.Context, u User, daemonID, harness string, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := `UPDATE daemon_harnesses SET enabled = ?, updated_at = ? WHERE daemon_id = ? AND harness = ?`
	args := []any{enabled, s.unix(), daemonID, harness}
	if !u.IsAdmin() {
		q += ` AND user_id = ?`
		args = append(args, u.ID)
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := s.failUnrunnable(ctx, tx, daemonID); err != nil {
		return err
	}
	return tx.Commit()
}
