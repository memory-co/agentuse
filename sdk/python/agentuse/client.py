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
    """agentuse 客户端。

    token 是 admin 令牌(服务密钥),只能放在网站后端。
    user 不为空时每个请求带 X-Agentuse-User,按那个用户的身份调用。
    """

    def __init__(self, base_url, token, user=None, timeout=30):
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.user = user
        self.timeout = timeout

    def as_user(self, user_id):
        """返回一个代为 user_id 调用的新客户端。"""
        return Client(self.base_url, self.token, user=user_id, timeout=self.timeout)

    def _request(self, method, path, body=None):
        headers = {"Authorization": f"Bearer {self.token}", "Accept": "application/json"}
        if self.user:
            headers[USER_HEADER] = self.user
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

    def me(self):
        return self._request("GET", "/api/me")

    def list_users(self):
        return self._request("GET", "/api/users")

    def create_user(self, id=None, name=None, role=None):
        return self._request("POST", "/api/users", _drop_none(id=id, name=name, role=role))

    def reset_user_token(self, user_id):
        return self._request("POST", f"/api/users/{_q(user_id)}/token")

    # agents

    def list_agents(self):
        return self._request("GET", "/api/agents")

    def create_agent(self, name, display_name=None, description=None):
        return self._request(
            "POST", "/api/agents", _drop_none(name=name, display_name=display_name, description=description)
        )

    def delete_agent(self, name):
        self._request("DELETE", f"/api/agents/{_q(name)}")

    # daemons

    def list_daemons(self):
        return self._request("GET", "/api/daemons")

    def create_daemon(self, name=None):
        """添加一台 daemon,返回的 setup_command 给用户复制到自己电脑上跑。"""
        return self._request("POST", "/api/daemons", _drop_none(name=name))

    def reset_daemon(self, daemon_id):
        return self._request("POST", f"/api/daemons/{_q(daemon_id)}/reset")

    def set_daemon_agent(self, daemon_id, agent, enabled):
        return self._request(
            "PUT", f"/api/daemons/{_q(daemon_id)}/agents/{_q(agent)}", {"enabled": bool(enabled)}
        )

    def delete_daemon(self, daemon_id):
        self._request("DELETE", f"/api/daemons/{_q(daemon_id)}")

    # sessions

    def list_sessions(self):
        return self._request("GET", "/api/sessions")

    def create_session(self, agent, prompt=None, input=None, cwd=None):
        """新建会话并发出第一条消息，返回 {"session": …, "message": …}。"""
        return self._request(
            "POST", "/api/sessions", _drop_none(agent=agent, prompt=prompt, input=input, cwd=cwd)
        )

    def send_message(self, session_id, prompt=None, input=None):
        """回复。会话不是 idle 时抛 AgentuseError(409)。"""
        return self._request(
            "POST", f"/api/sessions/{_q(session_id)}/messages", _drop_none(prompt=prompt, input=input)
        )

    def get_session(self, session_id):
        """返回 {"session": …, "messages": […], "events": […]}。"""
        return self._request("GET", f"/api/sessions/{_q(session_id)}")

    def delete_session(self, session_id):
        self._request("DELETE", f"/api/sessions/{_q(session_id)}")

    def wait_session(self, session_id, interval=2, timeout=None):
        """轮询直到会话回到 idle,返回最后一次 get_session 的结果。超时抛 TimeoutError。"""
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            detail = self.get_session(session_id)
            if detail["session"]["status"] == "idle":
                return detail
            if deadline is not None and time.monotonic() >= deadline:
                raise TimeoutError(f"agentuse: session {session_id} still {detail['session']['status']}")
            time.sleep(interval)


def _q(s):
    return urllib.parse.quote(str(s), safe="")


def _drop_none(**kw):
    return {k: v for k, v in kw.items() if v is not None}
