package service

import (
	"context"
	"database/sql"
	"errors"
	"github.com/memory-co/agentuse/server/internal/store"
	"strings"
)

// CreateDaemon 在页面上添加一台 daemon:生成一次性的安装令牌，等用户在电脑上跑 setup.sh 注册。
func (s *Service) CreateDaemon(ctx context.Context, userID, name string) (Daemon, error) {
	d := Daemon{
		ID:         randomID("dm_"),
		UserID:     userID,
		Name:       strings.TrimSpace(name),
		SetupToken: RandomToken("st_"),
		Status:     DaemonPending,
		CreatedAt:  s.unix(),
	}
	_, err := s.db.ExecContext(ctx,
		store.CreateDaemonInsert,
		d.ID, d.UserID, d.Name, d.SetupToken, d.Status, d.CreatedAt)
	return d, err
}

// RegisterDaemon 用安装令牌换长期密钥。令牌用一次就作废。
func (s *Service) RegisterDaemon(ctx context.Context, setupToken, hostname string) (Daemon, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Daemon{}, "", err
	}
	defer tx.Rollback()
	var d Daemon
	err = tx.QueryRowContext(ctx, store.RegisterDaemonSelect, setupToken).
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
		store.RegisterDaemonUpdate,
		d.Name, HashSecret(key), DaemonOffline, d.ID); err != nil {
		return Daemon{}, "", err
	}
	d.Status = DaemonOffline
	return d, key, tx.Commit()
}

// ResetDaemonSetup 重新生成安装令牌(用于换电脑 / 重装),旧密钥立即失效。
func (s *Service) resetDaemonSetup(ctx context.Context, u User, id string) (Daemon, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Daemon{}, err
	}
	defer tx.Rollback()
	query := store.ResetDaemonSetupSelect
	args := []any{id}
	if !u.IsAdmin() {
		query += store.ResetDaemonSetupClause
		args = append(args, u.ID)
	}
	var one int
	if err = tx.QueryRowContext(ctx, query, args...).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return Daemon{}, ErrNotFound
	} else if err != nil {
		return Daemon{}, err
	}
	if err = s.terminateDaemonRuns(ctx, tx, id, "daemon 已重置，请重新发送任务"); err != nil {
		return Daemon{}, err
	}
	if _, err = tx.ExecContext(ctx, store.ResetDaemonSetupUpdate, RandomToken("st_"), DaemonPending, id); err != nil {
		return Daemon{}, err
	}
	if err = tx.Commit(); err != nil {
		return Daemon{}, err
	}
	return s.GetDaemon(ctx, u, id)
}

func (s *Service) DaemonByKey(ctx context.Context, key string) (Daemon, error) {
	return s.scanDaemon(s.db.QueryRowContext(ctx, daemonCols+store.DaemonByKeyClause, HashSecret(key)))
}

func (s *Service) scanDaemon(row scanner) (Daemon, error) { return store.ScanDaemon(row) }

// GetDaemon 取一台 daemon 及其 harness 列表;非 admin 只能看自己的。
func (s *Service) GetDaemon(ctx context.Context, u User, id string) (Daemon, error) {
	q := daemonCols + store.GetDaemonClause
	args := []any{id}
	if !u.IsAdmin() {
		q += store.GetDaemonClause2
		args = append(args, u.ID)
	}
	d, err := s.scanDaemon(s.db.QueryRowContext(ctx, q, args...))
	if err != nil {
		return d, err
	}
	harnesses, err := s.daemonHarnesses(ctx, store.GetDaemonClause3, d.ID)
	d.Harnesses = harnesses
	if err == nil {
		d.Capabilities, err = s.daemonCapabilities(ctx, d.ID)
	}
	return d, err
}

// ListDaemons 列出用户的 daemon;admin 看所有人的。
func (s *Service) ListDaemons(ctx context.Context, u User) ([]Daemon, error) {
	q := daemonCols
	var args []any
	if !u.IsAdmin() {
		q += store.ListDaemonsClause
		args = append(args, u.ID)
	}
	rows, err := s.db.QueryContext(ctx, q+store.ListDaemonsClause2, args...)
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
		where, aargs = store.ListDaemonsClause3, []any{u.ID}
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

func (s *Service) daemonHarnesses(ctx context.Context, where string, args ...any) ([]DaemonHarness, error) {
	rows, err := s.db.QueryContext(ctx,
		store.DaemonHarnessesSelect+where+store.DaemonHarnessesClause, args...)
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
func (s *Service) deleteDaemon(ctx context.Context, u User, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := store.DeleteDaemonDelete
	args := []any{id}
	if !u.IsAdmin() {
		q += store.DeleteDaemonClause
		args = append(args, u.ID)
	}
	var one int
	check := strings.Replace(q, store.DeleteDaemonDelete2, store.DeleteDaemonSelect, 1)
	if err := tx.QueryRowContext(ctx, check, args...).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := s.terminateDaemonRuns(ctx, tx, id, "daemon 已删除"); err != nil {
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
func (s *Service) DaemonConnected(ctx context.Context, d Daemon, reported []ReportedHarness, caps ...[]string) (ignored []string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, store.DaemonConnectedDelete, d.ID); err != nil {
		return nil, err
	}
	if len(caps) > 0 {
		for _, c := range caps[0] {
			if _, err := tx.ExecContext(ctx, store.DaemonConnectedInsert, d.ID, c); err != nil {
				return nil, err
			}
		}
	}
	now := s.unix()
	if _, err := tx.ExecContext(ctx, store.DaemonConnectedUpdate, DaemonOnline, now, d.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, store.DaemonConnectedUpdate2, now, d.ID); err != nil {
		return nil, err
	}
	for _, a := range reported {
		var exists int
		if err := tx.QueryRowContext(ctx, store.DaemonConnectedSelect, a.Name).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			ignored = append(ignored, a.Name)
			continue
		}
		if _, err := tx.ExecContext(ctx,
			store.DaemonConnectedInsert2,
			d.ID, d.UserID, a.Name, a.Version, now); err != nil {
			return nil, err
		}
	}

	return ignored, tx.Commit()
}

func (s *Service) DaemonDisconnected(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, store.DaemonDisconnectedUpdate,
		DaemonOffline, s.unix(), id, DaemonOnline)
	return err
}

func (s *Service) TouchDaemon(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, store.TouchDaemonUpdate, s.unix(), id)
	return err
}

// MarkAllDaemonsOffline 在服务启动时调用:上次进程留下的 online 都不作数了。
func (s *Service) MarkAllDaemonsOffline(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, store.MarkAllDaemonsOfflineUpdate, DaemonOffline, DaemonOnline)
	return err
}

// SetDaemonHarnessEnabled 开关某台 daemon 上的某个 harness。只能开本机上报可用的。
// 关掉时，这个 harness 上排队中的会话直接判失败。
func (s *Service) setDaemonHarnessEnabled(ctx context.Context, u User, daemonID, harness string, enabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := store.SetDaemonHarnessEnabledUpdate
	args := []any{enabled, s.unix(), daemonID, harness}
	if !u.IsAdmin() {
		q += store.SetDaemonHarnessEnabledClause
		args = append(args, u.ID)
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return tx.Commit()
}
