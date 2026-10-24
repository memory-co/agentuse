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

// agentuse 客户端。token 是 admin 令牌(服务密钥),只能放在网站后端;
// user 不为空时每个请求带 X-Agentuse-User,按那个用户的身份调用。
export class Client {
  constructor({ baseUrl, token, user, fetch: fetchImpl } = {}) {
    if (!baseUrl) throw new Error("agentuse: baseUrl is required");
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.token = token;
    this.user = user;
    this._fetch = fetchImpl || globalThis.fetch;
  }

  // 返回一个代为 userId 调用的新客户端。
  asUser(userId) {
    return new Client({ baseUrl: this.baseUrl, token: this.token, user: userId, fetch: this._fetch });
  }

  async _request(method, path, body) {
    const headers = { Authorization: `Bearer ${this.token}`, Accept: "application/json" };
    if (this.user) headers[USER_HEADER] = this.user;
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
  me() { return this._request("GET", "/api/me"); }
  listUsers() { return this._request("GET", "/api/users"); }
  createUser(user = {}) { return this._request("POST", "/api/users", user); }
  resetUserToken(userId) { return this._request("POST", `/api/users/${q(userId)}/token`); }

  // agents
  listAgents() { return this._request("GET", "/api/agents"); }
  createAgent(agent) { return this._request("POST", "/api/agents", agent); }
  async deleteAgent(name) { await this._request("DELETE", `/api/agents/${q(name)}`); }

  // daemons
  listDaemons() { return this._request("GET", "/api/daemons"); }
  // 添加一台 daemon,返回的 setup_command 给用户复制到自己电脑上跑。
  createDaemon(name) { return this._request("POST", "/api/daemons", name ? { name } : {}); }
  resetDaemon(daemonId) { return this._request("POST", `/api/daemons/${q(daemonId)}/reset`); }
  setDaemonAgent(daemonId, agent, enabled) {
    return this._request("PUT", `/api/daemons/${q(daemonId)}/agents/${q(agent)}`, { enabled: !!enabled });
  }
  async deleteDaemon(daemonId) { await this._request("DELETE", `/api/daemons/${q(daemonId)}`); }

  // sessions
  listSessions() { return this._request("GET", "/api/sessions"); }
  // 新建会话并发出第一条消息:{ agent, prompt?, input?, cwd? } → { session, message }。
  createSession(req) { return this._request("POST", "/api/sessions", req); }
  // 回复:{ prompt? , input? }。会话不是 idle 时抛 AgentuseError(409)。
  sendMessage(sessionId, req) { return this._request("POST", `/api/sessions/${q(sessionId)}/messages`, req); }
  // → { session, messages, events }
  getSession(sessionId) { return this._request("GET", `/api/sessions/${q(sessionId)}`); }
  async deleteSession(sessionId) { await this._request("DELETE", `/api/sessions/${q(sessionId)}`); }

  // 轮询直到会话回到 idle,返回最后一次 getSession 的结果。超时抛错。
  async waitSession(sessionId, { interval = 2000, timeout } = {}) {
    const deadline = timeout === undefined ? Infinity : Date.now() + timeout;
    for (;;) {
      const detail = await this.getSession(sessionId);
      if (detail.session.status === "idle") return detail;
      if (Date.now() >= deadline) {
        throw new Error(`agentuse: session ${sessionId} still ${detail.session.status}`);
      }
      await new Promise((r) => setTimeout(r, interval));
    }
  }
}
