import { randomBytes } from "node:crypto";
// agentuse HTTP API 的薄封装，用 Node 18+ 自带的 fetch,没有依赖。接口见 docs/v2/apis。

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
  constructor({ baseUrl, token, fetch: fetchImpl } = {}) {
    if (!baseUrl) throw new Error("agentuse: baseUrl is required");
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.token = token;
    this._fetch = fetchImpl || globalThis.fetch;
  }

  async _request(method, path, user, body) {
    const headers = { Authorization: `Bearer ${this.token}`, Accept: "application/json" };
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
  createHarness(user, harness) { return this._request("POST", "/api/harnesses", user, harness); }
  async deleteHarness(user, name) { await this._request("DELETE", `/api/harnesses/${q(name)}`, user); }

  // agents
  listAgents(user) { return this._request("GET", "/api/agents", user); }
  // { id?, name, daemon_id, harness, description? };id 全局唯一，不传时服务生成。
  createAgent(user, agent) { return this._request("POST", "/api/agents", user, agent); }
  getAgent(user, agentId) { return this._request("GET", `/api/agents/${q(agentId)}`, user); }
  // 改名字、切换运行时、改说明:{ name?, daemon_id?, harness?, description? };daemon_id 和 harness 一起给。切换运行时只影响之后新建的会话。
  updateAgent(user, agentId, patch) { return this._request("PATCH", `/api/agents/${q(agentId)}`, user, patch); }
  async deleteAgent(user, agentId) { await this._request("DELETE", `/api/agents/${q(agentId)}`, user); }

  // daemons
  listDaemons(user) { return this._request("GET", "/api/daemons", user); }
  // 添加一台 daemon,返回的 setup_command 给用户复制到自己电脑上跑。
  createDaemon(user, name) { return this._request("POST", "/api/daemons", user, name ? { name } : {}); }
  resetDaemon(user, daemonId) { return this._request("POST", `/api/daemons/${q(daemonId)}/reset`, user); }
  setDaemonHarness(user, daemonId, harness, enabled) {
    return this._request("PUT", `/api/daemons/${q(daemonId)}/harnesses/${q(harness)}`, user, { enabled: !!enabled });
  }
  async deleteDaemon(user, daemonId) { await this._request("DELETE", `/api/daemons/${q(daemonId)}`, user); }

  async _attachmentRequest(method, path, user, body, contentType) {
    const headers = { Authorization: `Bearer ${this.token}` };
    if (user) headers[USER_HEADER] = user;
    if (contentType) headers["Content-Type"] = contentType;
    const res = await this._fetch(this.baseUrl + path, { method, headers, ...(body ? {body, duplex: "half"} : {}) });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      throw new AgentuseError(res.status, data.error || res.statusText);
    }
    return res;
  }
  async uploadAttachment(user, sessionId, filename, stream) {
    const boundary = "agentuse-" + randomBytes(24).toString("hex");
    async function* body() {
      yield Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="filename"\r\n\r\n${filename}\r\n--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="file"\r\nContent-Type: application/octet-stream\r\n\r\n`);
      const source = typeof stream.stream === "function" ? stream.stream() : stream;
      for await (const chunk of source) yield chunk;
      yield Buffer.from(`\r\n--${boundary}--\r\n`);
    }
    const res = await this._attachmentRequest("POST", `/api/sessions/${q(sessionId)}/attachments`, user, body(), `multipart/form-data; boundary=${boundary}`);
    return res.json();
  }
  listAttachments(user, sessionId, {limit, cursor} = {}) {
    const params = new URLSearchParams();
    if (limit !== undefined) params.set("limit", limit);
    if (cursor) params.set("cursor", cursor);
    return this._request("GET", `/api/sessions/${q(sessionId)}/attachments?${params}`, user);
  }
  getAttachment(user, sessionId, id) { return this._request("GET", `/api/sessions/${q(sessionId)}/attachments/${q(id)}`, user); }
  downloadAttachment(user, sessionId, id) { return this._attachmentRequest("GET", `/api/sessions/${q(sessionId)}/attachments/${q(id)}/content`, user); }
  deleteAttachment(user, sessionId, id) { return this._request("DELETE", `/api/sessions/${q(sessionId)}/attachments/${q(id)}`, user); }

  // sessions
  listSessions(user) { return this._request("GET", "/api/sessions", user); }
  // 新建会话并发出第一条消息:{ agent_id, prompt?, input? } → { session, message }。
  createSession(user, req) { return this._request("POST", "/api/sessions", user, req); }
  // 回复:{ prompt? , input? }。会话不是 idle 时抛 AgentuseError(409)。
  // 会话 idle 时返回 message;忙时是插话，返回 steer。
  sendMessage(user, sessionId, req) { return this._request("POST", `/api/sessions/${q(sessionId)}/messages`, user, req); }
  // → { session, messages, steers, questions, events }
  getSession(user, sessionId) { return this._request("GET", `/api/sessions/${q(sessionId)}`, user); }
  // 回答 harness 的提问(会话 waiting 时):answers 是 { 问题: 答案 },多选用 ", " 拼起来。已答过或过期抛 409。
  answerQuestion(user, sessionId, questionId, answers) {
    return this._request("POST", `/api/sessions/${q(sessionId)}/questions/${q(questionId)}/answer`, user, { answers });
  }
  async deleteSession(user, sessionId) { await this._request("DELETE", `/api/sessions/${q(sessionId)}`, user); }
  // 把 idle 的会话标记为已完成;再发消息就重新打开。
  resolveSession(user, sessionId) { return this._request("POST", `/api/sessions/${q(sessionId)}/resolve`, user); }
  reopenSession(user, sessionId) { return this._request("POST", `/api/sessions/${q(sessionId)}/reopen`, user); }

  // works:协调者 + 线程 + 共用说明
  listWorks(user) { return this._request("GET", "/api/works", user); }
  // { name, agent_id, instructions? } → { work, coordinator }。给协调者发消息用 sendMessage。
  createWork(user, req) { return this._request("POST", "/api/works", user, req); }
  getWork(user, workId) { return this._request("GET", `/api/works/${q(workId)}`, user); }
  updateWork(user, workId, patch) { return this._request("PATCH", `/api/works/${q(workId)}`, user, patch); }
  // 连协调者和所有线程一起删。
  async deleteWork(user, workId) { await this._request("DELETE", `/api/works/${q(workId)}`, user); }
  // 工作的线程(不含协调者)
  listWorkSessions(user, workId) { return this._request("GET", `/api/sessions?work_id=${q(workId)}`, user); }

  // 轮询直到会话回到 idle(或已完成)或在等回答提问(waiting),返回最后一次 getSession 的结果。超时抛错。
  async waitSession(user, sessionId, { interval = 2000, timeout } = {}) {
    const deadline = timeout === undefined ? Infinity : Date.now() + timeout;
    for (;;) {
      const detail = await this.getSession(user, sessionId);
      if (["idle", "waiting", "resolved"].includes(detail.session.status)) return detail;
      if (Date.now() >= deadline) {
        throw new Error(`agentuse: session ${sessionId} still ${detail.session.status}`);
      }
      await new Promise((r) => setTimeout(r, interval));
    }
  }
}
