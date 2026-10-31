package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/memory-co/agentuse/server/internal/store"
	"regexp"
	"strings"
	"unicode/utf8"
)

// agent id 全局唯一，建好后不能改。
var agentIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func cleanAgentName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("agent 名字必填")
	}
	if utf8.RuneCountInString(name) > 64 {
		return "", errors.New("agent 名字最长 64 个字")
	}
	return name, nil
}

// checkRuntime 确认 (daemonID, harness) 是 userID 自己的 daemon 上报过、而且现在可用并开启的 harness。
func (s *Service) checkRuntime(ctx context.Context, userID, daemonID, harness string) error {
	if daemonID == "" || harness == "" {
		return errors.New("daemon_id 和 harness 必填")
	}
	var one int
	err := s.db.QueryRowContext(ctx,
		store.CheckRuntimeSelect,
		daemonID, harness, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("daemon %q 上没有 harness %q(daemon 要是自己的，并且上报过这个 harness)", daemonID, harness)
	}
	if err != nil {
		return err
	}
	return runtimeErr(ctx, s.db, daemonID, harness)
}

// runtimeErr 检查运行时 (daemonID, harness) 现在能不能派活:这台 daemon 上报过这个 harness、最近一次上报里有、用户开启了。
// 能派返回 nil,否则返回给用户看的原因。daemon 离线不算：上线后会接着派。
func runtimeErr(ctx context.Context, q querier, daemonID, harness string) error {
	var available, enabled bool
	err := q.QueryRowContext(ctx,
		store.RuntimeErrSelect,
		daemonID, harness).Scan(&available, &enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w:daemon %s 上没有 harness %s", ErrRuntimeUnavailable, daemonID, harness)
	case err != nil:
		return err
	case !available:
		return fmt.Errorf("%w:daemon %s 最近一次连上时没检测到 harness %s,请在那台电脑上装好后重启 daemon", ErrRuntimeUnavailable, daemonID, harness)
	case !enabled:
		return fmt.Errorf("%w:daemon %s 上没有开启 harness %s,请先在 daemon 页面打开它", ErrRuntimeUnavailable, daemonID, harness)
	}
	return nil
}

func agentWriteErr(err error, id, name, harness string) error {
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), "agents.id"):
		return fmt.Errorf("agent id %q 已被占用", id)
	case strings.Contains(err.Error(), "UNIQUE"):
		return fmt.Errorf("已经有叫 %q 的 agent", name)
	case strings.Contains(err.Error(), "FOREIGN KEY"):
		return fmt.Errorf("harness %q 没有登记", harness)
	}
	return err
}

// CreateAgent 给用户建一个 agent。id 由调用方传入、全局唯一，不传时生成 ag_xxx;
// 同一用户下名字不能重复;运行时 (daemonID, harness) 必须是这个用户自己的 daemon 上报过的 harness。
func (s *Service) CreateAgent(ctx context.Context, userID, id, name, daemonID, harness, description string) (Agent, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = randomID("ag_")
	} else if !agentIDRe.MatchString(id) {
		return Agent{}, errors.New("agent id 只能用小写字母、数字、- 和 _,最长 64 个字符")
	}
	name, err := cleanAgentName(name)
	if err != nil {
		return Agent{}, err
	}
	daemonID, harness = strings.TrimSpace(daemonID), strings.TrimSpace(harness)
	if err := s.checkRuntime(ctx, userID, daemonID, harness); err != nil {
		return Agent{}, err
	}
	now := s.unix()
	a := Agent{ID: id, UserID: userID, Name: name, DaemonID: daemonID, Harness: harness, Description: description, CreatedAt: now, UpdatedAt: now}
	_, err = s.db.ExecContext(ctx,
		store.CreateAgentInsert,
		a.ID, a.UserID, a.Name, a.DaemonID, a.Harness, a.Description, now, now)
	if err != nil {
		return Agent{}, agentWriteErr(err, id, name, harness)
	}
	return a, nil
}

// UpdateAgent 改名字、切换运行时或改说明，只有主人(或 admin)能改。
// 切换运行时 DaemonID 和 Harness 要一起给;已有会话不受影响，只对之后新建的会话生效。
func (s *Service) UpdateAgent(ctx context.Context, u User, id string, p AgentPatch) (Agent, error) {
	a, err := s.GetAgent(ctx, u, id)
	if err != nil {
		return Agent{}, err
	}
	if p.Name != nil {
		if a.Name, err = cleanAgentName(*p.Name); err != nil {
			return Agent{}, err
		}
	}
	if (p.DaemonID == nil) != (p.Harness == nil) {
		return Agent{}, errors.New("切换运行时要 daemon_id 和 harness 一起给")
	}
	if p.DaemonID != nil {
		a.DaemonID, a.Harness = strings.TrimSpace(*p.DaemonID), strings.TrimSpace(*p.Harness)
		if err := s.checkRuntime(ctx, a.UserID, a.DaemonID, a.Harness); err != nil {
			return Agent{}, err
		}
	}
	if p.Description != nil {
		a.Description = *p.Description
	}
	a.UpdatedAt = s.unix()
	_, err = s.db.ExecContext(ctx,
		store.UpdateAgentUpdate,
		a.Name, nullIfEmpty(a.DaemonID), a.Harness, a.Description, a.UpdatedAt, a.ID)
	if err != nil {
		return Agent{}, agentWriteErr(err, a.ID, a.Name, a.Harness)
	}
	return a, nil
}

// DeleteAgent 删除 agent。用过它的会话保留，会话的 agent_id 变空。
func (s *Service) DeleteAgent(ctx context.Context, u User, id string) error {
	q := store.DeleteAgentDelete
	args := []any{id}
	if !u.IsAdmin() {
		q += store.DeleteAgentClause
		args = append(args, u.ID)
	}
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) GetAgent(ctx context.Context, u User, id string) (Agent, error) {
	q := agentCols + store.GetAgentClause
	args := []any{id}
	if !u.IsAdmin() {
		q += store.GetAgentClause2
		args = append(args, u.ID)
	}
	return scanAgent(s.db.QueryRowContext(ctx, q, args...))
}

// ListAgents 列 agent,按名字排序;admin 看所有人的。
func (s *Service) ListAgents(ctx context.Context, u User) ([]Agent, error) {
	q := agentCols
	var args []any
	if !u.IsAdmin() {
		q += store.ListAgentsClause
		args = append(args, u.ID)
	}
	q += store.ListAgentsClause2
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
