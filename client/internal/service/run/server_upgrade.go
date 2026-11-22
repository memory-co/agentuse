package run

import (
	"context"
	"errors"

	"github.com/memory-co/agentuse/internal/protocol"
)

var errUpgradeBusy = errors.New("Client 正在处理会话，请稍后重试")

func (d *endpointConnection) upgradeClient(ctx context.Context, request protocol.Message) {
	s := d.cfg.RunService
	reply := func(status, message string) error {
		return d.send(ctx, protocol.Message{Type: protocol.TypeClientUpgrade, ID: request.ID, TargetVersion: request.TargetVersion, Status: status, Error: message})
	}
	if s.Upgrade == nil {
		_ = reply(protocol.UpgradeFailed, "当前 Client 不支持远程升级")
		return
	}
	if s.localActiveCount() != 0 {
		_ = reply(protocol.UpgradeFailed, errUpgradeBusy.Error())
		return
	}
	if !s.upgradeInProgress.CompareAndSwap(false, true) {
		_ = reply(protocol.UpgradeFailed, "Client 正在处理另一项升级")
		return
	}
	err := s.Upgrade(ctx, request.TargetVersion, func() error {
		if !s.upgradeGate.TryLock() {
			return errUpgradeBusy
		}
		if s.localActiveCount() != 0 || len(s.slots) != 0 {
			s.upgradeGate.Unlock()
			return errUpgradeBusy
		}
		if err := reply(protocol.UpgradeRestarting, ""); err != nil {
			s.upgradeGate.Unlock()
			return err
		}
		// Keep new executions paused until the old process exits.
		return nil
	})
	if err != nil {
		s.upgradeInProgress.Store(false)
		if ctx.Err() == nil {
			_ = reply(protocol.UpgradeFailed, err.Error())
		}
	}
}
