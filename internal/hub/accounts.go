package hub

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/memory-co/agentuse/internal/models"
	"github.com/memory-co/agentuse/internal/store"
)

var (
	// ErrInvalid 是请求本身不对(handle 不合法、缺字段);错误信息说明哪里不对。
	ErrInvalid = errors.New("hub: invalid request")
	// ErrHandleTaken 表示 handle 在这个平台上已被占用。
	ErrHandleTaken = errors.New("hub: handle already taken")
	// ErrBadCode 表示接入码不存在、已用过或已过期。
	ErrBadCode = errors.New("hub: connect code invalid or expired")
	// ErrUnauthorized 表示设备 id / 密钥不对,或设备已被吊销。
	ErrUnauthorized = errors.New("hub: unauthorized device")
)

// NewAgent 是平台替用户建 agent 的请求。
type NewAgent struct {
	Owner         string               `json:"owner"`      // 主人的 user_id
	OwnerName     string               `json:"owner_name"` // 主人的显示名
	Handle        string               `json:"handle"`
	AgentUserID   string               `json:"agent_user_id"`
	Wake          models.WakeMode      `json:"wake,omitempty"` // 默认 owner
	Actions       []models.Action      `json:"actions,omitempty"`
	WorkspaceMode models.WorkspaceMode `json:"workspace_mode,omitempty"`
	Driver        string               `json:"driver,omitempty"` // 默认 claude
}

// AgentPatch 是改 agent 的请求;nil 字段不改。
type AgentPatch struct {
	Wake          *models.WakeMode      `json:"wake,omitempty"`
	Actions       *[]models.Action      `json:"actions,omitempty"`
	WorkspaceMode *models.WorkspaceMode `json:"workspace_mode,omitempty"`
	AgentUserID   *string               `json:"agent_user_id,omitempty"`
}

// ConnectInfo 是一条接入码和用它的命令。码只在这里出现一次,hub 只存哈希。
type ConnectInfo struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	Command   string    `json:"command"`
}

// AgentView 是给平台展示的 agent:agent 本身 + 在线状态 + 绑定的设备。
type AgentView struct {
	models.Agent
	Online bool        `json:"online"`
	Device *DeviceView `json:"device,omitempty"`
}

// DeviceView 是设备对外展示的部分(不含密钥哈希)。
type DeviceView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	OS       string     `json:"os,omitempty"`
	Online   bool       `json:"online"`
	LastSeen *time.Time `json:"last_seen,omitempty"`
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func validWake(w models.WakeMode) bool {
	return w == models.WakeOwner || w == models.WakeMembers
}

func validMode(m models.WorkspaceMode) bool {
	return m == "" || m == models.WorkspaceReadOnly || m == models.WorkspaceReadWrite
}

// CreateAgent 替平台上的一个用户建 agent,并发第一条接入码。
func (h *Hub) CreateAgent(ctx context.Context, platformID string, req NewAgent) (AgentView, ConnectInfo, error) {
	p, err := h.Platform(platformID)
	if err != nil {
		return AgentView{}, ConnectInfo{}, err
	}
	req.Handle = strings.ToLower(strings.TrimSpace(req.Handle))
	switch {
	case req.Owner == "":
		return AgentView{}, ConnectInfo{}, invalid("owner is required")
	case !models.ValidHandle(req.Handle):
		return AgentView{}, ConnectInfo{}, invalid("handle must be 2-40 of a-z 0-9 - _, starting and ending with a letter or digit")
	case req.AgentUserID == "":
		return AgentView{}, ConnectInfo{}, invalid("agent_user_id is required")
	case !validMode(req.WorkspaceMode):
		return AgentView{}, ConnectInfo{}, invalid("workspace_mode must be readonly or readwrite")
	}
	if req.Wake == "" {
		req.Wake = models.WakeOwner
	}
	if !validWake(req.Wake) {
		return AgentView{}, ConnectInfo{}, invalid("wake must be owner or members")
	}
	if req.Driver == "" {
		req.Driver = "claude"
	}
	if req.Driver != "claude" {
		return AgentView{}, ConnectInfo{}, invalid("driver must be claude")
	}
	now := h.now()
	a := models.Agent{
		ID: models.NewID("ag_"), Platform: p.ID, Handle: req.Handle, AgentUserID: req.AgentUserID, Owner: req.Owner,
		Driver: req.Driver, Wake: req.Wake, Actions: req.Actions, WorkspaceMode: req.WorkspaceMode,
		CreatedAt: now, UpdatedAt: now,
	}
	// 存的是按平台策略截断后的,平台看到的就是实际生效的。
	if len(a.Actions) > 0 {
		a.Actions, _ = a.Effective(p.Policy)
	}
	if err := h.store.CreateAgent(ctx, a); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return AgentView{}, ConnectInfo{}, ErrHandleTaken
		}
		return AgentView{}, ConnectInfo{}, err
	}
	if err := h.store.PutUser(ctx, models.User{Platform: p.ID, ID: req.Owner, Name: req.OwnerName, CreatedAt: now, UpdatedAt: now}); err != nil {
		return AgentView{}, ConnectInfo{}, err
	}
	info, err := h.issueCode(ctx, a)
	if err != nil {
		return AgentView{}, ConnectInfo{}, err
	}
	return h.view(ctx, a), info, nil
}

// agentOf 找平台里的一个 agent;别的平台的 agent 当作不存在。
func (h *Hub) agentOf(ctx context.Context, platformID, id string) (models.Agent, error) {
	if _, err := h.Platform(platformID); err != nil {
		return models.Agent{}, err
	}
	a, err := h.store.GetAgent(ctx, id)
	if err != nil {
		return a, err
	}
	if a.Platform != platformID {
		return models.Agent{}, store.ErrNotFound
	}
	return a, nil
}

// Agent 是平台查一个 agent。
func (h *Hub) Agent(ctx context.Context, platformID, id string) (AgentView, error) {
	a, err := h.agentOf(ctx, platformID, id)
	if err != nil {
		return AgentView{}, err
	}
	return h.view(ctx, a), nil
}

// Agents 列平台里的 agent;owner 非空时只列这个人的。
func (h *Hub) Agents(ctx context.Context, platformID, owner string) ([]AgentView, error) {
	if _, err := h.Platform(platformID); err != nil {
		return nil, err
	}
	list, err := h.store.ListAgents(ctx, store.AgentFilter{Platform: platformID, Owner: owner})
	if err != nil {
		return nil, err
	}
	out := make([]AgentView, len(list))
	for i, a := range list {
		out[i] = h.view(ctx, a)
	}
	return out, nil
}

// NewConnectCode 给 agent 发一条新接入码(换电脑、上一条过期了),同时作废旧的。
func (h *Hub) NewConnectCode(ctx context.Context, platformID, id string) (ConnectInfo, error) {
	a, err := h.agentOf(ctx, platformID, id)
	if err != nil {
		return ConnectInfo{}, err
	}
	return h.issueCode(ctx, a)
}

func (h *Hub) issueCode(ctx context.Context, a models.Agent) (ConnectInfo, error) {
	code := models.NewSecret("ac_")
	exp := h.now().Add(models.ConnectCodeTTL)
	if err := h.store.PutConnectCode(ctx, models.ConnectCode{Hash: models.HashSecret(code), Platform: a.Platform, AgentID: a.ID, ExpiresAt: exp}); err != nil {
		return ConnectInfo{}, err
	}
	return ConnectInfo{Code: code, ExpiresAt: exp, Command: ConnectCommand(h.opts.PublicURL, code)}, nil
}

// ConnectCommand 是展示给用户、在自己电脑的仓库目录里跑的那条命令。
func ConnectCommand(hubURL, code string) string {
	if hubURL == "" {
		hubURL = "<hub 地址>"
	}
	return "agentuse connect " + strings.TrimRight(hubURL, "/") + " " + code
}

// UpdateAgent 改 agent 的唤醒规则、动作、现场权限或应用用户 id。
func (h *Hub) UpdateAgent(ctx context.Context, platformID, id string, patch AgentPatch) (AgentView, error) {
	p, err := h.Platform(platformID)
	if err != nil {
		return AgentView{}, err
	}
	a, err := h.agentOf(ctx, platformID, id)
	if err != nil {
		return AgentView{}, err
	}
	if patch.Wake != nil {
		if !validWake(*patch.Wake) {
			return AgentView{}, invalid("wake must be owner or members")
		}
		a.Wake = *patch.Wake
	}
	if patch.Actions != nil {
		a.Actions = *patch.Actions
		if len(a.Actions) > 0 {
			a.Actions, _ = a.Effective(p.Policy)
		}
	}
	if patch.WorkspaceMode != nil {
		if !validMode(*patch.WorkspaceMode) {
			return AgentView{}, invalid("workspace_mode must be readonly or readwrite")
		}
		a.WorkspaceMode = *patch.WorkspaceMode
	}
	if patch.AgentUserID != nil {
		if *patch.AgentUserID == "" {
			return AgentView{}, invalid("agent_user_id cannot be empty")
		}
		a.AgentUserID = *patch.AgentUserID
	}
	a.UpdatedAt = h.now()
	if err := h.store.PutAgent(ctx, a); err != nil {
		return AgentView{}, err
	}
	return h.view(ctx, a), nil
}

// DeleteAgent 删 agent:排队中和进行中的运行判失败(进行中的那台设备下次心跳就会停下),会话留作记录。
func (h *Hub) DeleteAgent(ctx context.Context, platformID, id string) error {
	a, err := h.agentOf(ctx, platformID, id)
	if err != nil {
		return err
	}
	if _, err := h.store.FailRuns(ctx, store.FailFilter{AgentID: a.ID, Statuses: []models.RunStatus{models.RunQueued, models.RunClaimed}},
		"agent deleted", h.now()); err != nil {
		return err
	}
	return h.store.DeleteAgent(ctx, a.ID)
}

// RevokeDevice 吊销一台设备:它的密钥立刻作废,绑在上面的 agent 变成未接入。
func (h *Hub) RevokeDevice(ctx context.Context, platformID, id string) error {
	if _, err := h.Platform(platformID); err != nil {
		return err
	}
	d, err := h.store.GetDevice(ctx, id)
	if err != nil {
		return err
	}
	if d.Platform != platformID {
		return store.ErrNotFound
	}
	now := h.now()
	if d.RevokedAt == nil {
		d.RevokedAt = &now
		if err := h.store.PutDevice(ctx, d); err != nil {
			return err
		}
	}
	list, err := h.store.ListAgents(ctx, store.AgentFilter{DeviceID: d.ID})
	if err != nil {
		return err
	}
	for _, a := range list {
		if err := h.unbind(ctx, a); err != nil {
			return err
		}
	}
	h.signal() // 让这台设备的推送连接醒来发现自己被吊销
	return nil
}

// unbind 把 agent 从设备上解绑,变成未接入。
func (h *Hub) unbind(ctx context.Context, a models.Agent) error {
	a.DeviceID, a.Workdir, a.BaseRef, a.UpdatedAt = "", "", "", h.now()
	return h.store.PutAgent(ctx, a)
}

// AuthDevice 校验设备 id 和密钥,记一次「见到」(在线状态)。吊销的设备返回 ErrUnauthorized。
func (h *Hub) AuthDevice(ctx context.Context, id, key string) (models.Device, error) {
	d, err := h.checkDevice(ctx, id, key)
	if err != nil {
		return d, err
	}
	now := h.now()
	h.mu.Lock()
	h.seen[id] = now
	h.mu.Unlock()
	// last_seen 落库最多一分钟一次,轮询模式下不至于每次领取都写库。
	if d.LastSeen == nil || now.Sub(*d.LastSeen) > time.Minute {
		d.LastSeen = &now
		if err := h.store.PutDevice(ctx, d); err != nil {
			return d, err
		}
	}
	return d, nil
}

// checkDevice 只校验设备 id 和密钥,不算「见到」。
func (h *Hub) checkDevice(ctx context.Context, id, key string) (models.Device, error) {
	if id == "" || key == "" {
		return models.Device{}, ErrUnauthorized
	}
	d, err := h.store.GetDevice(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return d, ErrUnauthorized
	}
	if err != nil {
		return d, err
	}
	if d.RevokedAt != nil || subtle.ConstantTimeCompare([]byte(models.HashSecret(key)), []byte(d.KeyHash)) != 1 {
		return models.Device{}, ErrUnauthorized
	}
	return d, nil
}

// Revoked 报告设备是否已被吊销(或不存在)。推送连接在队列变动时用它检查自己还能不能连着。
func (h *Hub) Revoked(ctx context.Context, id string) bool {
	d, err := h.store.GetDevice(ctx, id)
	return err != nil || d.RevokedAt != nil
}

// Attach 记一条设备的推送连接;连接断开时调用交回的函数。
func (h *Hub) Attach(deviceID string) (detach func()) {
	h.mu.Lock()
	h.conns[deviceID]++
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.conns[deviceID]--
		if h.conns[deviceID] <= 0 {
			delete(h.conns, deviceID)
		}
		h.seen[deviceID] = h.now()
	}
}

// Online 报告设备是否在线:有推送连接,或 OnlineWindow 内请求过(轮询模式)。
func (h *Hub) Online(deviceID string) bool {
	if deviceID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[deviceID] > 0 {
		return true
	}
	t, ok := h.seen[deviceID]
	return ok && h.now().Sub(t) < OnlineWindow
}

func (h *Hub) view(ctx context.Context, a models.Agent) AgentView {
	v := AgentView{Agent: a}
	if a.DeviceID == "" {
		return v
	}
	v.Online = h.Online(a.DeviceID)
	if d, err := h.store.GetDevice(ctx, a.DeviceID); err == nil {
		v.Device = h.deviceView(d)
	}
	return v
}

func (h *Hub) deviceView(d models.Device) *DeviceView {
	v := &DeviceView{ID: d.ID, Name: d.Name, OS: d.OS, Online: h.Online(d.ID), LastSeen: d.LastSeen}
	h.mu.Lock()
	if t, ok := h.seen[d.ID]; ok && (v.LastSeen == nil || t.After(*v.LastSeen)) {
		v.LastSeen = &t
	}
	h.mu.Unlock()
	return v
}

// ConnectRequest 是用户电脑上 agentuse connect 发来的兑换请求。
type ConnectRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
	OS         string `json:"os,omitempty"`
	Dir        string `json:"dir"`                // agent 的工作目录(git 仓库)
	BaseRef    string `json:"base_ref,omitempty"` // 默认 main
	// 这台电脑在这个 hub 上已有的设备身份。其中有同平台、同主人的就复用,不再发新密钥。
	Devices []DeviceCred `json:"devices,omitempty"`
}

// DeviceCred 是一台设备的身份。
type DeviceCred struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

// ConnectResponse 是兑换结果。DeviceKey 只在新建设备时出现,且只出现这一次。
type ConnectResponse struct {
	Platform  string       `json:"platform"`
	Agent     models.Agent `json:"agent"`
	DeviceID  string       `json:"device_id"`
	DeviceKey string       `json:"device_key,omitempty"`
}

// Connect 兑换接入码:把码对应的 agent 绑到这台电脑和目录上。
// 同一主人在这台电脑上已有设备就复用;agent 原来绑在别的设备上的,就此搬过来。
func (h *Hub) Connect(ctx context.Context, req ConnectRequest) (ConnectResponse, error) {
	if req.Code == "" {
		return ConnectResponse{}, ErrBadCode
	}
	if strings.TrimSpace(req.Dir) == "" {
		return ConnectResponse{}, invalid("dir is required")
	}
	// 先认设备再消耗接入码:设备凭据不对不该浪费一条码。认不出来的(吊销了、密钥不对)忽略。
	var known []models.Device
	for _, c := range req.Devices {
		if d, err := h.checkDevice(ctx, c.ID, c.Key); err == nil {
			known = append(known, d)
		}
	}
	now := h.now()
	code, err := h.store.TakeConnectCode(ctx, models.HashSecret(req.Code), now)
	if errors.Is(err, store.ErrNotFound) {
		return ConnectResponse{}, ErrBadCode
	}
	if err != nil {
		return ConnectResponse{}, err
	}
	a, err := h.store.GetAgent(ctx, code.AgentID)
	if errors.Is(err, store.ErrNotFound) {
		return ConnectResponse{}, ErrBadCode
	}
	if err != nil {
		return ConnectResponse{}, err
	}

	res := ConnectResponse{Platform: a.Platform}
	var d models.Device
	for _, k := range known {
		if k.Platform == a.Platform && k.Owner == a.Owner {
			d = k
			break
		}
	}
	if d.ID == "" {
		key := models.NewSecret("dk_")
		d = models.Device{ID: models.NewID("dv_"), Platform: a.Platform, Owner: a.Owner, KeyHash: models.HashSecret(key), CreatedAt: now}
		res.DeviceKey = key
	}
	d.Name, d.OS, d.LastSeen = req.DeviceName, req.OS, &now
	if err := h.store.PutDevice(ctx, d); err != nil {
		return ConnectResponse{}, err
	}

	a.DeviceID, a.Workdir, a.BaseRef, a.UpdatedAt = d.ID, req.Dir, req.BaseRef, now
	if a.BaseRef == "" {
		a.BaseRef = "main"
	}
	if err := h.store.PutAgent(ctx, a); err != nil {
		return ConnectResponse{}, err
	}
	h.signal() // 排着等这个 agent 的运行现在有设备可领了
	res.Agent, res.DeviceID = a, d.ID
	return res, nil
}

// DeviceInfo 是设备自己看:设备信息和绑在上面的 agent。
func (h *Hub) DeviceInfo(ctx context.Context, deviceID string) (DeviceView, []models.Agent, error) {
	d, err := h.store.GetDevice(ctx, deviceID)
	if err != nil {
		return DeviceView{}, nil, err
	}
	agents, err := h.store.ListAgents(ctx, store.AgentFilter{DeviceID: deviceID})
	return *h.deviceView(d), agents, err
}

// Disconnect 是设备主动把一个 agent 从自己身上解绑(agentuse disconnect)。
func (h *Hub) Disconnect(ctx context.Context, d models.Device, handle string) error {
	a, err := h.store.AgentByHandle(ctx, d.Platform, strings.ToLower(handle))
	if err != nil {
		return err
	}
	if a.DeviceID != d.ID {
		return store.ErrNotFound
	}
	return h.unbind(ctx, a)
}
