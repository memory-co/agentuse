"""agentuse HTTP API 的薄封装，只用标准库。接口见 docs/v2/apis。"""

import json
import time
import urllib.error
import urllib.parse
import urllib.request

USER_HEADER = "X-Agentuse-User"


class AgentuseError(Exception):
    """非 2xx 响应。status 是 HTTP 状态码,message 是响应里的 error。"""

    def __init__(self, status, message):
        super().__init__(f"agentuse: {status} {message}")
        self.status = status
        self.message = message


class Client:
    """agentuse 客户端，不带状态，可以全局共用一个。

    token 是 admin 令牌(服务密钥),只能放在网站后端。
    除了用户管理，每个方法第一个参数是网站的 user id,请求带 X-Agentuse-User
    按那个用户的身份执行;传 None 就是 admin 本身。
    """

    def __init__(self, base_url, token, timeout=30):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def _request(self, method, path, user=None, body=None):
        headers = {"Authorization": f"Bearer {self.token}", "Accept": "application/json"}
        if user:
            headers[USER_HEADER] = user
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base_url + path, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read()
        except urllib.error.HTTPError as e:
            raw = e.read()
            try:
                msg = json.loads(raw).get("error") or e.reason
            except ValueError:
                msg = raw.decode(errors="replace") or e.reason
            raise AgentuseError(e.code, msg) from None
        return json.loads(raw) if raw else None

    # auth / users

    def me(self, user):
        return self._request("GET", "/api/me", user)

    def list_users(self):
        return self._request("GET", "/api/users")

    def create_user(self, id=None, name=None, role=None):
        return self._request("POST", "/api/users", None, _drop_none(id=id, name=name, role=role))

    def reset_user_token(self, user_id):
        return self._request("POST", f"/api/users/{_q(user_id)}/token")

    # harnesses

    def list_harnesses(self, user):
        return self._request("GET", "/api/harnesses", user)

    def create_harness(self, user, name, display_name=None, description=None):
        return self._request(
            "POST", "/api/harnesses", user, _drop_none(name=name, display_name=display_name, description=description)
        )

    def delete_harness(self, user, name):
        self._request("DELETE", f"/api/harnesses/{_q(name)}", user)

    # agents

    def list_agents(self, user):
        return self._request("GET", "/api/agents", user)

    def create_agent(self, user, name, daemon_id, harness, description=None, id=None):
        """id 全局唯一，不传时服务生成 ag_xxx。"""
        return self._request(
            "POST", "/api/agents", user, _drop_none(id=id, name=name, daemon_id=daemon_id, harness=harness, description=description)
        )

    def get_agent(self, user, agent_id):
        return self._request("GET", f"/api/agents/{_q(agent_id)}", user)

    def update_agent(self, user, agent_id, name=None, daemon_id=None, harness=None, description=None):
        """改名字、切换运行时(daemon_id 和 harness 一起给)、改说明;None 不改。切换运行时只影响之后新建的会话。"""
        return self._request(
            "PATCH", f"/api/agents/{_q(agent_id)}", user, _drop_none(name=name, daemon_id=daemon_id, harness=harness, description=description)
        )

    def delete_agent(self, user, agent_id):
        self._request("DELETE", f"/api/agents/{_q(agent_id)}", user)

    # daemons

    def list_daemons(self, user):
        return self._request("GET", "/api/daemons", user)

    def create_daemon(self, user, name=None):
        """添加一台 daemon,返回的 setup_command 给用户复制到自己电脑上跑。"""
        return self._request("POST", "/api/daemons", user, _drop_none(name=name))

    def reset_daemon(self, user, daemon_id):
        return self._request("POST", f"/api/daemons/{_q(daemon_id)}/reset", user)

    def set_daemon_harness(self, user, daemon_id, harness, enabled):
        return self._request(
            "PUT", f"/api/daemons/{_q(daemon_id)}/harnesses/{_q(harness)}", user, {"enabled": bool(enabled)}
        )

    def delete_daemon(self, user, daemon_id):
        self._request("DELETE", f"/api/daemons/{_q(daemon_id)}", user)

    # sessions

    def list_sessions(self, user):
        return self._request("GET", "/api/sessions", user)

    def create_session(self, user, agent_id, prompt=None, input=None):
        """用一个 agent 新建会话并发出第一条消息，返回 {"session": …, "message": …}。"""
        return self._request(
            "POST", "/api/sessions", user, _drop_none(agent_id=agent_id, prompt=prompt, input=input)
        )

    def send_message(self, user, session_id, prompt=None, input=None):
        """回复。会话不是 idle 时抛 AgentuseError(409)。"""
        return self._request(
            "POST", f"/api/sessions/{_q(session_id)}/messages", user, _drop_none(prompt=prompt, input=input)
        )

    def get_session(self, user, session_id):
        """返回 {"session": …, "messages": […], "events": […]}。"""
        return self._request("GET", f"/api/sessions/{_q(session_id)}", user)

    def delete_session(self, user, session_id):
        self._request("DELETE", f"/api/sessions/{_q(session_id)}", user)

    def wait_session(self, user, session_id, interval=2, timeout=None):
        """轮询直到会话回到 idle,返回最后一次 get_session 的结果。超时抛 TimeoutError。"""
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            detail = self.get_session(user, session_id)
            if detail["session"]["status"] == "idle":
                return detail
            if deadline is not None and time.monotonic() >= deadline:
                raise TimeoutError(f"agentuse: session {session_id} still {detail['session']['status']}")
            time.sleep(interval)


def _q(s):
    return urllib.parse.quote(str(s), safe="")


def _drop_none(**kw):
    return {k: v for k, v in kw.items() if v is not None}
