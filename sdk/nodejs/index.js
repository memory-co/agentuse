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

  // sessions
  listSessions(user) { return this._request("GET", "/api/sessions", user); }
  // 新建会话并发出第一条消息:{ agent_id, prompt?, input? } → { session, message }。
  createSession(user, req) { return this._request("POST", "/api/sessions", user, req); }
  // 回复:{ prompt? , input? }。会话不是 idle 时抛 AgentuseError(409)。
  // 会话 idle 时返回 message;忙时是插话，返回 steer。
  sendMessage(user, sessionId, req) { return this._request("POST", `/api/sessions/${q(sessionId)}/messages`, user, req); }
  // → { session, messages, steers, events }
  getSession(user, sessionId) { return this._request("GET", `/api/sessions/${q(sessionId)}`, user); }
  async deleteSession(user, sessionId) { await this._request("DELETE", `/api/sessions/${q(sessionId)}`, user); }

  // 轮询直到会话回到 idle,返回最后一次 getSession 的结果。超时抛错。
  async waitSession(user, sessionId, { interval = 2000, timeout } = {}) {
    const deadline = timeout === undefined ? Infinity : Date.now() + timeout;
    for (;;) {
      const detail = await this.getSession(user, sessionId);
      if (detail.session.status === "idle") return detail;
      if (Date.now() >= deadline) {
        throw new Error(`agentuse: session ${sessionId} still ${detail.session.status}`);
      }
      await new Promise((r) => setTimeout(r, interval));
    }
  }
}
