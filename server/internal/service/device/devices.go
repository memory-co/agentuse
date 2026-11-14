package device

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
	"github.com/memory-co/agentuse/server/internal/store"
)

// CreateDevice 在页面上添加一台 device:生成一次性的安装令牌，等用户在电脑上跑 setup.sh 注册。
func (s *Service) CreateDevice(ctx context.Context, userID, name string) (agentuse.Device, error) {
	d := agentuse.Device{
		ID:         store.RandomID("dev_"),
		UserID:     userID,
		Name:       strings.TrimSpace(name),
		SetupToken: store.RandomToken("st_"),
		Status:     agentuse.DevicePending,
		CreatedAt:  s.Now().Unix(),
	}
	_, err := s.DB.ExecContext(ctx,
		store.CreateDeviceInsert,
		d.ID, d.UserID, d.Name, d.SetupToken, d.Status, d.CreatedAt)
	if err == nil {
		s.Changes.Publish(notification.Change{Resource: "devices", ID: d.ID}, d.UserID)
	}
	return d, err
}

// RegisterDevice 用安装令牌换长期密钥。令牌用一次就作废。
func (s *Service) RegisterDevice(ctx context.Context, setupToken, hostname string) (agentuse.Device, string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return agentuse.Device{}, "", err
	}
	defer tx.Rollback()
	var d agentuse.Device
	err = tx.QueryRowContext(ctx, store.RegisterDeviceSelect, setupToken).
		Scan(&d.ID, &d.UserID, &d.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return agentuse.Device{}, "", store.ErrNotFound
	}
	if err != nil {
		return agentuse.Device{}, "", err
	}
	if d.Name == "" {
		d.Name = strings.TrimSpace(hostname)
	}
	if d.Name == "" {
		d.Name = d.ID
	}
	key := store.RandomToken("dk_")
	if _, err := tx.ExecContext(ctx,
		store.RegisterDeviceUpdate,
		d.Name, store.HashSecret(key), agentuse.DeviceOffline, d.ID); err != nil {
		return agentuse.Device{}, "", err
	}
	d.Status = agentuse.DeviceOffline
	err = tx.Commit()
	if err == nil {
		s.Changes.Publish(notification.Change{Resource: "devices", ID: d.ID}, d.UserID)
	}
	return d, key, err
}

// ResetDeviceSetup 重新生成安装令牌(用于换电脑 / 重装),旧密钥立即失效。
func (s *Service) resetDeviceSetup(ctx context.Context, u agentuse.User, id string) (agentuse.Device, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return agentuse.Device{}, err
	}
	defer tx.Rollback()
	query := store.ResetDeviceSetupSelect
	args := []any{id}
	if !u.IsAdmin() {
		query += store.ResetDeviceSetupClause
		args = append(args, u.ID)
	}
	var one int
	if err = tx.QueryRowContext(ctx, query, args...).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return agentuse.Device{}, store.ErrNotFound
	} else if err != nil {
		return agentuse.Device{}, err
	}
	if err = s.RunService.RetireDeviceDeliveries(ctx, tx, id, "device 已重置，请重新发送任务"); err != nil {
		return agentuse.Device{}, err
	}
	if _, err = tx.ExecContext(ctx, store.ResetDeviceSetupUpdate, store.RandomToken("st_"), agentuse.DevicePending, id); err != nil {
		return agentuse.Device{}, err
	}
	if err = tx.Commit(); err != nil {
		return agentuse.Device{}, err
	}
	return s.GetDevice(ctx, u, id)
}

func (s *Service) DeviceByKey(ctx context.Context, key string) (agentuse.Device, error) {
	return s.scanDevice(s.DB.QueryRowContext(ctx, store.DeviceCols+store.DeviceByKeyClause, store.HashSecret(key)))
}

func (s *Service) scanDevice(row store.Scanner) (agentuse.Device, error) {
	return store.ScanDevice(row)
}

// GetDevice 取一台 device 及其 harness 列表;非 admin 只能看自己的。
func (s *Service) GetDevice(ctx context.Context, u agentuse.User, id string) (agentuse.Device, error) {
	q := store.DeviceCols + store.GetDeviceClause
	args := []any{id}
	if !u.IsAdmin() {
		q += store.GetDeviceClause2
		args = append(args, u.ID)
	}
	d, err := s.scanDevice(s.DB.QueryRowContext(ctx, q, args...))
	if err != nil {
		return d, err
	}
	harnesses, err := s.deviceHarnesses(ctx, store.GetDeviceClause3, d.ID)
	d.Harnesses = harnesses
	if err == nil {
		d.Capabilities, err = s.deviceCapabilities(ctx, d.ID)
	}
	return d, err
}

// ListDevices 列出用户的 device;admin 看所有人的。
func (s *Service) ListDevices(ctx context.Context, u agentuse.User) ([]agentuse.Device, error) {
	q := store.DeviceCols
	var args []any
	if !u.IsAdmin() {
		q += store.ListDevicesClause
		args = append(args, u.ID)
	}
	rows, err := s.DB.QueryContext(ctx, q+store.ListDevicesClause2, args...)
	if err != nil {
		return nil, err
	}
	out := []agentuse.Device{}
	byID := map[string]int{}
	for rows.Next() {
		d, err := s.scanDevice(rows)
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
		where, aargs = store.ListDevicesClause3, []any{u.ID}
	}
	harnesses, err := s.deviceHarnesses(ctx, where, aargs...)
	if err != nil {
		return nil, err
	}
	for _, a := range harnesses {
		if i, ok := byID[a.DeviceID]; ok {
			out[i].Harnesses = append(out[i].Harnesses, a)
		}
	}
	for i := range out {
		caps, e := s.deviceCapabilities(ctx, out[i].ID)
		if e != nil {
			return nil, e
		}
		out[i].Capabilities = caps
	}
	return out, nil
}

func (s *Service) deviceHarnesses(ctx context.Context, where string, args ...any) ([]agentuse.DeviceHarness, error) {
	rows, err := s.DB.QueryContext(ctx,
		store.DeviceHarnessesSelect+where+store.DeviceHarnessesClause, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentuse.DeviceHarness{}
	for rows.Next() {
		var a agentuse.DeviceHarness
		var ver sql.NullString
		var capabilities string
		if err := rows.Scan(&a.DeviceID, &a.UserID, &a.Harness, &ver, &a.HarnessRevision, &capabilities, &a.Available, &a.Enabled, &a.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(capabilities), &a.Capabilities); err != nil {
			return nil, err
		}
		a.Version = ver.String
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteDevice revokes credentials without manufacturing native run results.
func (s *Service) deleteDevice(ctx context.Context, u agentuse.User, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := store.DeleteDeviceDelete
	args := []any{id}
	if !u.IsAdmin() {
		q += store.DeleteDeviceClause
		args = append(args, u.ID)
	}
	var one int
	check := strings.Replace(q, store.DeleteDeviceDelete2, store.DeleteDeviceSelect, 1)
	if err := tx.QueryRowContext(ctx, check, args...).Scan(&one); errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	} else if err != nil {
		return err
	}
	if err := s.RunService.RetireDeviceDeliveries(ctx, tx, id, "device 已删除"); err != nil {
		return err
	}

	for _, query := range []string{`DELETE FROM device_harnesses WHERE device_id=?`, `DELETE FROM device_capabilities WHERE device_id=?`} {
		if _, err := tx.ExecContext(ctx, query, id); err != nil {
			return err
		}
	}
	if err := s.ConversationService.DetachDeviceAttachments(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// DeviceConnected 处理 device 连上:标在线，按上报更新 device_harnesses。
// 上报里有的标 available=1 并保留原来的 enabled;没上报的标 available=0。
// Harness 标识直接来自 client；server 不维护适配器注册表或原生执行配置。
func (s *Service) DeviceConnected(ctx context.Context, d agentuse.Device, reported []ReportedHarness, caps ...[]string) (ignored []string, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, store.DeviceConnectedDelete, d.ID); err != nil {
		return nil, err
	}
	if len(caps) > 0 {
		for _, c := range caps[0] {
			if _, err := tx.ExecContext(ctx, store.DeviceConnectedInsert, d.ID, c); err != nil {
				return nil, err
			}
		}
	}
	now := s.Now().Unix()
	if _, err := tx.ExecContext(ctx, store.DeviceConnectedUpdate, agentuse.DeviceOnline, now, d.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, store.DeviceConnectedUpdate2, now, d.ID); err != nil {
		return nil, err
	}
	for _, a := range reported {
		if strings.TrimSpace(a.Name) == "" || len(a.Name) > 128 || len(a.Version) > 512 {
			return nil, fmt.Errorf("invalid reported harness identity")
		}
		capabilities, err := capabilityNames(a.Capabilities)
		if err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(capabilities)
		if _, err := tx.ExecContext(ctx,
			store.DeviceConnectedInsert2,
			d.ID, d.UserID, a.Name, a.Version, a.Revision, string(encoded), now); err != nil {
			return nil, err
		}
	}

	err = tx.Commit()
	if err == nil {
		s.Changes.Publish(notification.Change{Resource: "devices", ID: d.ID}, d.UserID)
	}
	return ignored, err
}

func (s *Service) DeviceDisconnected(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, store.DeviceDisconnectedUpdate,
		agentuse.DeviceOffline, s.Now().Unix(), id, agentuse.DeviceOnline)
	if err == nil {
		if d, e := s.GetDevice(ctx, agentuse.User{Role: agentuse.RoleAdmin}, id); e == nil {
			s.Changes.Publish(notification.Change{Resource: "devices", ID: id}, d.UserID)
		}
	}
	return err
}

func (s *Service) TouchDevice(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, store.TouchDeviceUpdate, s.Now().Unix(), id)
	return err
}

// MarkAllDevicesOffline 在服务启动时调用:上次进程留下的 online 都不作数了。
func (s *Service) MarkAllDevicesOffline(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, store.MarkAllDevicesOfflineUpdate, agentuse.DeviceOffline, agentuse.DeviceOnline)
	return err
}

// SetDeviceHarnessEnabled 开关某台 device 上的某个 harness。只能开本机上报可用的。
// 关闭后暂停派发；实际 run 的终态仍由 Client 上报。
func (s *Service) setDeviceHarnessEnabled(ctx context.Context, u agentuse.User, deviceID, harness string, enabled bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := store.SetDeviceHarnessEnabledUpdate
	args := []any{enabled, s.Now().Unix(), deviceID, harness}
	if !u.IsAdmin() {
		q += store.SetDeviceHarnessEnabledClause
		args = append(args, u.ID)
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return store.ErrNotFound
	}

	return tx.Commit()
}

func capabilityNames(ids []string) ([]string, error) {
	out := []string{}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || strings.TrimSpace(id) != id {
			return nil, fmt.Errorf("capability 名称 必须是非空且无首尾空格的字符串")
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	return out, nil
}
