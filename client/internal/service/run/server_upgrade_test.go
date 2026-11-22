package run

import (
	"context"
	"errors"
	"testing"

	"github.com/memory-co/agentuse/internal/protocol"
)

func TestClientUpgradeProtectsExecutions(t *testing.T) {
	request := protocol.Message{Type: protocol.TypeClientUpgrade, ID: "u:test", TargetVersion: "v9.1.0"}
	t.Run("already executing", func(t *testing.T) {
		s := &Service{
			localRuns: map[string]*localExecution{"session": {}},
			Upgrade: func(context.Context, string, func() error) error {
				t.Fatal("an active execution allowed an upgrade")
				return nil
			},
		}
		d := &endpointConnection{cfg: Config{RunService: s}}
		d.upgradeClient(context.Background(), request)
	})
	t.Run("work starts during download", func(t *testing.T) {
		s := &Service{}
		s.Upgrade = func(_ context.Context, _ string, beforeRestart func() error) error {
			s.upgradeGate.RLock()
			defer s.upgradeGate.RUnlock()
			err := beforeRestart()
			if !errors.Is(err, errUpgradeBusy) {
				t.Fatal("the upgrade began restarting before the work was finished")
			}
			return err
		}
		d := &endpointConnection{cfg: Config{RunService: s}}
		d.upgradeClient(context.Background(), request)
		if s.upgradeInProgress.Load() {
			t.Fatal("the Client cannot retry an interrupted upgrade")
		}
	})
}
