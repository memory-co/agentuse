"""agentuse HTTP API 的薄封装，只用标准库。接口见 docs/v2/apis。"""

import json
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import TypedDict, Literal, Optional, List

class _AttachmentOptional(TypedDict, total=False):
    daemon_id: str
    created_by_run_id: str
    created_by_agent_key: str


class Attachment(_AttachmentOptional):
    id: str
    session_id: str
    filename: str
    content_type: str
    size_bytes: int
    sha256: str
    created_by: str
    source: Literal["user", "daemon", "work"]
    created_at: int
    download_path: str


class AttachmentPage(TypedDict):
    attachments: List[Attachment]
    next_cursor: Optional[str]


class Run(TypedDict, total=False):
    id: str
    session_id: str
    message_id: int
    agent_id: str
    agent_key: str
    agent_name: str
    daemon_id: str
    harness: str
    status: Literal["pending", "running", "waiting", "succeeded", "failed", "cancelled"]
    attempt_id: str
    lease_until: int
    context_seq: int
    result_message_id: int
    error: str
    created_at: int
    updated_at: int


class Delivery(TypedDict, total=False):
    id: int
    message_id: int
    agent_key: str
    run_id: str
    kind: Literal["new_run", "steer"]
    status: str
    error: str


class RunDetail(TypedDict):
    run: Run
    events: List[dict]


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

    def _request(self, method, path, user=None, body=None, extra_headers=None):
        headers = {**(extra_headers or {}), "Authorization": f"Bearer {self.token}", "Accept": "application/json"}
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

    def _attachment_request(self, method, path, user, body=None, content_type=None):
        headers = {"Authorization": f"Bearer {self.token}"}
        if user:
            headers[USER_HEADER] = user
        if content_type:
            headers["Content-Type"] = content_type
        req = urllib.request.Request(self.base_url + path, data=body, headers=headers, method=method)
        try:
            return urllib.request.urlopen(req, timeout=self.timeout)
        except urllib.error.HTTPError as e:
            with e:
                raw = e.read()
            try:
                msg = json.loads(raw).get("error") or e.reason
            except ValueError:
                msg = raw.decode(errors="replace") or e.reason
            raise AgentuseError(e.code, msg) from None

    def upload_attachment(self, user, session_id, filename, stream) -> Attachment:
        """Stream a binary file object without buffering it in memory."""
        boundary = "agentuse-" + secrets.token_hex(24)
        def body():
            yield (f'--{boundary}\r\nContent-Disposition: form-data; name="filename"\r\n\r\n'
                   f'{filename}\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="file"\r\n'
                   'Content-Type: application/octet-stream\r\n\r\n').encode()
            while True:
                chunk = stream.read(65536)
                if not chunk:
                    break
                yield chunk
            yield f'\r\n--{boundary}--\r\n'.encode()
        with self._attachment_request("POST", f"/api/sessions/{_q(session_id)}/attachments", user,
                                      body(), f"multipart/form-data; boundary={boundary}") as resp:
            return json.load(resp)

    def list_attachments(self, user, session_id, limit=None, cursor=None) -> AttachmentPage:
        query = urllib.parse.urlencode(_drop_none(limit=limit, cursor=cursor))
        return self._request("GET", f"/api/sessions/{_q(session_id)}/attachments?{query}", user)

    def get_attachment(self, user, session_id, attachment_id) -> Attachment:
        return self._request("GET", f"/api/sessions/{_q(session_id)}/attachments/{_q(attachment_id)}", user)

    def download_attachment(self, user, session_id, attachment_id):
        """Return a response stream. Use it as a context manager or close it."""
        return self._attachment_request("GET", f"/api/sessions/{_q(session_id)}/attachments/{_q(attachment_id)}/content", user)

    def delete_attachment(self, user, session_id, attachment_id):
        self._request("DELETE", f"/api/sessions/{_q(session_id)}/attachments/{_q(attachment_id)}", user)

    # sessions

    def list_sessions(self, user):
        return self._request("GET", "/api/sessions", user)

    def create_session(self, user, prompt=None, input=None, defer_start=None, mode=None, title=None, target_agent_ids=None):
        """创建共享会话，可用 target_agent_ids 指定首条消息的执行者。"""
        return self._request(
            "POST", "/api/sessions", user, _drop_none(prompt=prompt, input=input, defer_start=defer_start, mode=mode, title=title, target_agent_ids=target_agent_ids)
        )

    def send_message(self, user, session_id, prompt=None, input=None, target_agent_ids=None, idempotency_key=None):
        """发送共享消息；返回 message、runs 和 deliveries。"""
        return self._request(
            "POST", f"/api/sessions/{_q(session_id)}/messages", user, _drop_none(prompt=prompt, input=input, target_agent_ids=target_agent_ids), extra_headers={"Idempotency-Key": idempotency_key} if idempotency_key else None
        )

    def get_session(self, user, session_id):
        """返回 {"session": …, "messages": […], "steers": […], "questions": […], "events": […]}。"""
        return self._request("GET", f"/api/sessions/{_q(session_id)}", user)

    def delete_session(self, user, session_id):
        self._request("DELETE", f"/api/sessions/{_q(session_id)}", user)

    def resolve_session(self, user, session_id):
        """把 idle 的会话标记为已完成;再发消息就重新打开。"""
        return self._request("POST", f"/api/sessions/{_q(session_id)}/resolve", user)

    def reopen_session(self, user, session_id):
        return self._request("POST", f"/api/sessions/{_q(session_id)}/reopen", user)

    def get_run(self, user, session_id, run_id) -> RunDetail:
        return self._request("GET", f"/api/sessions/{_q(session_id)}/runs/{_q(run_id)}", user)

    def cancel_run(self, user, session_id, run_id) -> Run:
        return self._request("POST", f"/api/sessions/{_q(session_id)}/runs/{_q(run_id)}/cancel", user)


    def answer_run_question(self, user, session_id, run_id, question_id, answers):
        return self._request("POST", f"/api/sessions/{_q(session_id)}/runs/{_q(run_id)}/questions/{_q(question_id)}/answer", user, {"answers": answers})

    def wait_run(self, user, session_id, run_id, interval=2, timeout=None, return_on_waiting=False) -> RunDetail:
        start = time.monotonic()
        while True:
            detail = self.get_run(user, session_id, run_id)
            if detail["run"]["status"] in ("succeeded", "failed", "cancelled") or (return_on_waiting and detail["run"]["status"] == "waiting"):
                return detail
            if timeout is not None and time.monotonic() - start >= timeout:
                raise TimeoutError("agentuse: wait_run timed out")
            time.sleep(interval)

    def wait_message_runs(self, user, session_id, message_id, interval=2, timeout=None):
        start = time.monotonic()
        while True:
            detail = self.get_session(user, session_id)
            if all(d["status"] in ("succeeded", "failed", "cancelled") for d in detail.get("deliveries", []) if d["message_id"] == message_id):
                return detail
            if timeout is not None and time.monotonic() - start >= timeout:
                raise TimeoutError("agentuse: wait_message_runs timed out")
            time.sleep(interval)

    # works:协调者 + 线程 + 共用说明

    def list_works(self, user):
        return self._request("GET", "/api/works", user)

    def create_work(self, user, name, agent_id, instructions=None, mode=None):
        """建工作，同时建出协调者会话，返回 {"work": …, "coordinator": …}。给协调者发消息用 send_message。"""
        return self._request("POST", "/api/works", user, _drop_none(name=name, agent_id=agent_id, instructions=instructions, mode=mode))

    def get_work(self, user, work_id):
        return self._request("GET", f"/api/works/{_q(work_id)}", user)

    def update_work(self, user, work_id, name=None, instructions=None, coordinator_agent_id=None):
        return self._request("PATCH", f"/api/works/{_q(work_id)}", user, _drop_none(name=name, instructions=instructions, coordinator_agent_id=coordinator_agent_id))

    def delete_work(self, user, work_id):
        """连协调者和所有线程一起删。"""
        self._request("DELETE", f"/api/works/{_q(work_id)}", user)

    def list_work_sessions(self, user, work_id):
        """列工作的线程(不含协调者)。"""
        return self._request("GET", f"/api/sessions?work_id={_q(work_id)}", user)

    def wait_session(self, user, session_id, interval=2, timeout=None, return_on_waiting=False):
        """轮询直到会话回到 idle(或已完成)或在等回答提问(waiting),返回最后一次 get_session 的结果。超时抛 TimeoutError。"""
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            detail = self.get_session(user, session_id)
            if detail["session"]["status"] in ("idle", "resolved") or (detail["session"]["status"] == "waiting" and (return_on_waiting)):
                return detail
            if deadline is not None and time.monotonic() >= deadline:
                raise TimeoutError(f"agentuse: session {session_id} still {detail['session']['status']}")
            time.sleep(interval)


def _q(s):
    return urllib.parse.quote(str(s), safe="")


def _drop_none(**kw):
    return {k: v for k, v in kw.items() if v is not None}
