import { randomBytes } from "node:crypto";
// agentuse HTTP API 的薄封装，用 Node 18+ 自带的 fetch,没有依赖。接口见 docs/server/conversations.md。

export const USER_HEADER = "X-Agentuse-User";

// 非 2xx 响应。status 是 HTTP 状态码,message 是响应里的 error。
export class AgentuseError extends Error {
  constructor(status, message) {
    super(`agentuse: ${status} ${message}`);
    this.name = "AgentuseError";
    this.status = status;
  }
}

const q = encodeURIComponent;

// agentuse 客户端，不带状态，可以全局共用一个。token 是 admin 令牌(服务密钥),只能放在网站后端。
// 除了用户管理，每个方法第一个参数是网站的 user id,请求带 X-Agentuse-User
// 按那个用户的身份执行;传 null 就是 admin 本身。
export class Client {
  constructor({ baseUrl, token, source, fetch: fetchImpl } = {}) {
    if (!baseUrl) throw new Error("agentuse: baseUrl is required");
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.token = token;
    this.source = source;
    this._fetch = fetchImpl || globalThis.fetch;
  }

  _sourceHeaders() {
    if (!this.source) return {};
    return {"X-Agentuse-Conversation": this.source.conversation_id, "X-Agentuse-Agent": this.source.agent_id, "X-Agentuse-Session": this.source.session_id, "X-Agentuse-Run": this.source.run_id};
  }

  async _request(method, path, user, body, extraHeaders = {}) {
    const headers = { ...this._sourceHeaders(), ...extraHeaders, Authorization: `Bearer ${this.token}`, Accept: "application/json" };
    if (user) headers[USER_HEADER] = user;
    const init = { method, headers };
    if (body !== undefined) {
      headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await this._fetch(this.baseUrl + path, init);
    const text = await res.text();
    if (!res.ok) {
      let msg = text || res.statusText;
      try {
        msg = JSON.parse(text).error || msg;
      } catch {}
      throw new AgentuseError(res.status, msg);
    }
    return text ? JSON.parse(text) : undefined;
  }

  // auth / users
  me(user) { return this._request("GET", "/api/me", user); }
  listUsers() { return this._request("GET", "/api/users"); }
  createUser(user = {}) { return this._request("POST", "/api/users", null, user); }
  resetUserToken(userId) { return this._request("POST", `/api/users/${q(userId)}/token`); }

  // harnesses
  listHarnesses(user) { return this._request("GET", "/api/harnesses", user); }

  // agents
  listAgents(user) { return this._request("GET", "/api/agents", user); }
  // { id?, name, device_id, harness, description? };id 全局唯一，不传时服务生成。
  createAgent(user, agent) { return this._request("POST", "/api/agents", user, agent); }
  getAgent(user, agentId) { return this._request("GET", `/api/agents/${q(agentId)}`, user); }
  // 改名字、切换运行时、改说明:{ name?, device_id?, harness?, description? };device_id 和 harness 一起给。切换运行时只影响之后新建的会话。
  publishAgentVersion(user, agentId, config) { return this._request("POST", `/api/agents/${q(agentId)}/versions`, user, config); }
  listAgentVersions(user, agentId) { return this._request("GET", `/api/agents/${q(agentId)}/versions`, user); }
  getAgentVersion(user, agentId, versionId) { return this._request("GET", `/api/agents/${q(agentId)}/versions/${q(versionId)}`, user); }
  listSkills(user) { return this._request("GET", "/api/skills", user); }
  listSkillVersions(user, skillId) { return this._request("GET", `/api/skills/${q(skillId)}/versions`, user); }
  deleteSkill(user, skillId) { return this._request("DELETE", `/api/skills/${q(skillId)}`, user); }
  async deleteAgent(user, agentId) { await this._request("DELETE", `/api/agents/${q(agentId)}`, user); }

  // devices
  listDevices(user) { return this._request("GET", "/api/devices", user); }
  // 添加一台 device,返回的 setup_command 给用户复制到自己电脑上跑。
  createDevice(user, name) { return this._request("POST", "/api/devices", user, name ? { name } : {}); }
  resetDevice(user, deviceId) { return this._request("POST", `/api/devices/${q(deviceId)}/reset`, user); }
  setDeviceHarness(user, deviceId, harness, enabled) {
    return this._request("PUT", `/api/devices/${q(deviceId)}/harnesses/${q(harness)}`, user, { enabled: !!enabled });
  }
  async deleteDevice(user, deviceId) { await this._request("DELETE", `/api/devices/${q(deviceId)}`, user); }

  async _attachmentRequest(method, path, user, body, contentType) {
    const headers = { ...this._sourceHeaders(), Authorization: `Bearer ${this.token}` };
    if (user) headers[USER_HEADER] = user;
    if (contentType) headers["Content-Type"] = contentType;
    const res = await this._fetch(this.baseUrl + path, { method, headers, ...(body ? {body, duplex: "half"} : {}) });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new AgentuseError(res.status, data.error || res.statusText);
    }
    return res;
  }
  async publishSkill(user, skillId, baseVersionId, stream) {
    const boundary = "agentuse-" + randomBytes(24).toString("hex");
    async function* body() {
      yield Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="base_version_id"\r\n\r\n${baseVersionId || ""}\r\n--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="skill.zip"\r\nContent-Type: application/zip\r\n\r\n`);
      const source = typeof stream.stream === "function" ? stream.stream() : stream;
      for await (const chunk of source) yield chunk;
      yield Buffer.from(`\r\n--${boundary}--\r\n`);
    }
    const path = skillId ? `/api/skills/${q(skillId)}/versions` : "/api/skills";
    return (await this._attachmentRequest("POST", path, user, body(), `multipart/form-data; boundary=${boundary}`)).json();
  }
  getSkill(user, skillId) { return this._request("GET", `/api/skills/${q(skillId)}`, user); }
  getSkillVersion(user, skillId, versionId) { return this._request("GET", `/api/skills/${q(skillId)}/versions/${q(versionId)}`, user); }
  downloadSkillPackage(user, skillId, versionId) { return this._attachmentRequest("GET", `/api/skills/${q(skillId)}/versions/${q(versionId)}/package`, user); }
  async uploadAttachment(user, conversationId, filename, stream) {
    const boundary = "agentuse-" + randomBytes(24).toString("hex");
    async function* body() {
      yield Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="filename"\r\n\r\n${filename}\r\n--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="file"\r\nContent-Type: application/octet-stream\r\n\r\n`);
      const source = typeof stream.stream === "function" ? stream.stream() : stream;
      for await (const chunk of source) yield chunk;
      yield Buffer.from(`\r\n--${boundary}--\r\n`);
    }
    const res = await this._attachmentRequest("POST", `/api/conversations/${q(conversationId)}/attachments`, user, body(), `multipart/form-data; boundary=${boundary}`);
    return res.json();
  }
  listAttachments(user, conversationId, {limit, cursor} = {}) {
    const params = new URLSearchParams();
    if (limit !== undefined) params.set("limit", limit);
    if (cursor) params.set("cursor", cursor);
    return this._request("GET", `/api/conversations/${q(conversationId)}/attachments?${params}`, user);
  }
  getAttachment(user, conversationId, id) { return this._request("GET", `/api/conversations/${q(conversationId)}/attachments/${q(id)}`, user); }
  downloadAttachment(user, conversationId, id) { return this._attachmentRequest("GET", `/api/conversations/${q(conversationId)}/attachments/${q(id)}/content`, user); }
  deleteAttachment(user, conversationId, id) { return this._request("DELETE", `/api/conversations/${q(conversationId)}/attachments/${q(id)}`, user); }

  // conversations
  listConversations(user) { return this._request("GET", "/api/conversations", user); }
  // 创建共享会话，可带首条消息与 to_agent_ids、to_user_ids。
  createConversation(user, req) { return this._request("POST", "/api/conversations", user, req); }
  // 回复:{ prompt? , input? }。会话不是 idle 时抛 AgentuseError(409)。
  // 会话 idle 时返回 message;忙时是插话，返回 steer。
  sendMessage(user, conversationId, req) { return this._request("POST", `/api/conversations/${q(conversationId)}/messages`, user, req); }
  // → { conversation, messages, runs, deliveries, questions, events }
  getConversation(user, conversationId) { return this._request("GET", `/api/conversations/${q(conversationId)}`, user); }
  // 回答 harness 的提问(会话 waiting 时):answers 是 { 问题: 答案 },多选用 ", " 拼起来。已答过或过期抛 409。
  async deleteConversation(user, conversationId) { await this._request("DELETE", `/api/conversations/${q(conversationId)}`, user); }
  // 把 idle 的会话标记为已完成;再发消息就重新打开。
  resolveConversation(user, conversationId) { return this._request("POST", `/api/conversations/${q(conversationId)}/resolve`, user); }
  reopenConversation(user, conversationId) { return this._request("POST", `/api/conversations/${q(conversationId)}/reopen`, user); }

  getRun(user, conversation, run) { return this._request("GET", `/api/conversations/${q(conversation)}/runs/${q(run)}`, user); }
  cancelRun(user, conversation, run) { return this._request("POST", `/api/conversations/${q(conversation)}/runs/${q(run)}/cancel`, user); }
  replyRunRequest(user, conversation, run, request, answer) { return this._request("POST", `/api/conversations/${q(conversation)}/runs/${q(run)}/requests/${q(request)}/reply`, user, {answer}); }
  async waitRun(user, conversation, run, {interval=2000, timeout, returnOnWaiting=false}={}) {
    const start=Date.now();
    for (;;) { const d=await this.getRun(user,conversation,run); if (["completed","failed","cancelled","rejected"].includes(d.run.status) || (returnOnWaiting && d.run.status==="waiting")) return d;
      if (timeout!=null && Date.now()-start>=timeout) throw new Error("agentuse: waitRun timed out"); await new Promise(r=>setTimeout(r,interval)); }
  }
  async waitMessageRuns(user, conversation, message, {interval=2000,timeout}={}) {
    const start=Date.now();
    for (;;) { const d=await this.getConversation(user,conversation); if ((d.deliveries||[]).filter(x=>x.message_id===message).every(x=>["completed","failed","cancelled","rejected"].includes(x.status))) return d;
      if (timeout!=null && Date.now()-start>=timeout) throw new Error("agentuse: waitMessageRuns timed out"); await new Promise(r=>setTimeout(r,interval)); }
  }

  listChildren(user, parentId) { return this._request("GET", `/api/conversations?parent_conversation_id=${q(parentId)}`, user); }
  sendParentMessage(user, conversationId, req) { return this._request("POST", `/api/conversations/${q(conversationId)}/parent/messages`, user, req); }
  async deleteConversationTree(user, conversationId) { await this._request("DELETE", `/api/conversations/${q(conversationId)}?recursive=true`, user); }

  async waitConversation(user, conversationId, { interval = 2000, timeout, returnOnWaiting = false } = {}) {
    const deadline = timeout === undefined ? Infinity : Date.now() + timeout;
    for (;;) {
      const detail = await this.getConversation(user, conversationId);
      if (["idle", "resolved"].includes(detail.conversation.status) || (detail.conversation.status === "waiting" && (returnOnWaiting))) return detail;
      if (Date.now() >= deadline) {
        throw new Error(`agentuse: conversation ${conversationId} still ${detail.conversation.status}`);
      }
      await new Promise((r) => setTimeout(r, interval));
    }
  }
}
