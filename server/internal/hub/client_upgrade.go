package hub

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/memory-co/agentuse/internal/protocol"
	"github.com/memory-co/agentuse/internal/version"
	agentuse "github.com/memory-co/agentuse/sdk/golang"
	"github.com/memory-co/agentuse/server/internal/notification"
)

type clientUpgrade struct {
	id         string
	connection *deviceConn
	view       agentuse.ClientUpgrade
}

func (u clientUpgrade) active() bool {
	return u.view.Status == protocol.UpgradeDownloading || u.view.Status == protocol.UpgradeRestarting
}

func (h *Hub) ClientUpgrade(id string) *agentuse.ClientUpgrade {
	h.mu.Lock()
	defer h.mu.Unlock()
	u, ok := h.clientUpgrades[id]
	if !ok {
		return nil
	}
	return &u.view
}

// UpgradeClient sends an official release tag to the currently authenticated device.
func (h *Hub) UpgradeClient(ctx context.Context, deviceID, target string) (agentuse.ClientUpgrade, error) {
	h.mu.Lock()
	c := h.conns[deviceID]
	if c == nil || !c.canUpgrade {
		h.mu.Unlock()
		return agentuse.ClientUpgrade{}, errors.New("设备离线或 Client 不支持远程升级")
	}
	if !version.Newer(target, c.d.ClientVersion) {
		h.mu.Unlock()
		return agentuse.ClientUpgrade{}, errors.New("当前 Client 没有可升级的正式版本")
	}
	if u := h.clientUpgrades[deviceID]; u.active() {
		h.mu.Unlock()
		return u.view, nil
	}
	u := clientUpgrade{id: "u:" + uuid.NewString(), connection: c, view: agentuse.ClientUpgrade{TargetVersion: target, Status: protocol.UpgradeDownloading}}
	h.clientUpgrades[deviceID] = u
	h.mu.Unlock()
	h.publishDevice(c)

	if err := c.send(ctx, protocol.Message{Type: protocol.TypeClientUpgrade, ID: u.id, TargetVersion: target}); err != nil {
		h.updateClientUpgrade(c, u.id, protocol.UpgradeFailed, "升级指令发送失败")
		return agentuse.ClientUpgrade{}, err
	}
	time.AfterFunc(7*time.Minute, func() {
		h.updateClientUpgrade(c, u.id, protocol.UpgradeFailed, "等待 Client 升级上线超时")
	})
	return u.view, nil
}

func (h *Hub) publishDevice(c *deviceConn) {
	h.service.BrowserChanges.Publish(notification.Change{Resource: "devices", ID: c.d.ID}, c.d.UserID)
}

func (h *Hub) reportClientUpgrade(c *deviceConn, m protocol.Message) {
	h.mu.Lock()
	u := h.clientUpgrades[c.d.ID]
	if h.conns[c.d.ID] != c || u.connection != c || u.id != m.ID || u.view.TargetVersion != m.TargetVersion || !u.active() {
		h.mu.Unlock()
		return
	}
	u.view.Status, u.view.Error = m.Status, m.Error
	h.clientUpgrades[c.d.ID] = u
	h.mu.Unlock()
	h.publishDevice(c)
}

func (h *Hub) updateClientUpgrade(c *deviceConn, id, status, message string) {
	h.mu.Lock()
	u := h.clientUpgrades[c.d.ID]
	if u.id != id || !u.active() {
		h.mu.Unlock()
		return
	}
	u.view.Status, u.view.Error = status, message
	h.clientUpgrades[c.d.ID] = u
	h.mu.Unlock()
	h.publishDevice(c)
}

func (h *Hub) clientReconnected(c *deviceConn) {
	h.mu.Lock()
	u := h.clientUpgrades[c.d.ID]
	h.mu.Unlock()
	if !u.active() || u.connection == c {
		return
	}
	if c.d.ClientVersion == u.view.TargetVersion {
		h.updateClientUpgrade(c, u.id, protocol.UpgradeSucceeded, "")
	} else {
		h.updateClientUpgrade(c, u.id, protocol.UpgradeFailed, "Client 重新连接后仍未运行目标版本")
	}
}
