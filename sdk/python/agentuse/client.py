"""agentuse HTTP API 的薄封装，只用标准库。接口见 docs/server/conversations.md。"""

import json
import secrets
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import TypedDict, Literal, Optional, List

class _AttachmentOptional(TypedDict, total=False):
    device_id: str
    created_by_run_id: str
    created_by_agent_id: str


class Attachment(_AttachmentOptional):
    id: str
    conversation_id: str
    relative_path: str
    filename: str
    content_type: str
    size_bytes: int
    sha256: str
    created_by: str
    source: Literal["user", "device", "work"]
    created_at: int
    download_path: str


class AttachmentPage(TypedDict):
    attachments: List[Attachment]
    next_cursor: Optional[str]


class Run(TypedDict, total=False):
    id: str
    conversation_id: str
    first_message_id: str
    agent_id: str
    agent_version_id: str
    device_id: str
    session_id: str
    harness: str
    harness_revision: str
    status: Literal["running", "waiting", "completed", "failed", "cancelled"]
    result_message_id: str
    output: str
    error: str
    result: dict
    created_at: int
    updated_at: int

class Delivery(TypedDict, total=False):
    message_id: str
    conversation_id: str
    agent_id: str
    agent_version_id: str
    device_id: str
    harness: str
    content: str
    run_id: str
    input_id: str
    session_id: str
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

    # agents

    def list_agents(self, user):
        return self._request("GET", "/api/agents", user)

    def create_agent(self, user, name, device_id, harness, description=None, id=None, model=None, instructions="", skills=None):
        """id 全局唯一，不传时服务生成 ag_xxx。"""
        return self._request(
            "POST", "/api/agents", user, _drop_none(id=id, name=name, device_id=device_id, harness=harness, description=description, model=model, instructions=instructions, skills=skills or [])
        )

    def get_agent(self, user, agent_id):
        return self._request("GET", f"/api/agents/{_q(agent_id)}", user)

    def publish_agent_version(self, user, agent_id, config):
        return self._request("POST", f"/api/agents/{_q(agent_id)}/versions", user, config)

    def list_agent_versions(self, user, agent_id):
        return self._request("GET", f"/api/agents/{_q(agent_id)}/versions", user)

    def get_agent_version(self, user, agent_id, version_id):
        return self._request("GET", f"/api/agents/{_q(agent_id)}/versions/{_q(version_id)}", user)

    def list_skills(self, user):
        return self._request("GET", "/api/skills", user)

    def list_skill_versions(self, user, skill_id):
        return self._request("GET", f"/api/skills/{_q(skill_id)}/versions", user)

    def delete_skill(self, user, skill_id):
        return self._request("DELETE", f"/api/skills/{_q(skill_id)}", user)

    def delete_agent(self, user, agent_id):
        self._request("DELETE", f"/api/agents/{_q(agent_id)}", user)

    # devices

    def list_devices(self, user):
        return self._request("GET", "/api/devices", user)

    def create_device(self, user, name=None):
        """添加一台 device,返回的 setup_command 给用户复制到自己电脑上跑。"""
        return self._request("POST", "/api/devices", user, _drop_none(name=name))

    def reset_device(self, user, device_id):
        return self._request("POST", f"/api/devices/{_q(device_id)}/reset", user)

    def set_device_harness(self, user, device_id, harness, enabled):
        return self._request(
            "PUT", f"/api/devices/{_q(device_id)}/harnesses/{_q(harness)}", user, {"enabled": bool(enabled)}
        )

    def delete_device(self, user, device_id):
        self._request("DELETE", f"/api/devices/{_q(device_id)}", user)

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

    def get_skill(self, user, skill_id):
        return self._request("GET", f"/api/skills/{_q(skill_id)}", user)

    def get_skill_version(self, user, skill_id, version_id):
        return self._request("GET", f"/api/skills/{_q(skill_id)}/versions/{_q(version_id)}", user)

    def publish_skill(self, user, skill_id, base_version_id, stream):
        """Create when skill_id is None; otherwise publish against base_version_id."""
        boundary = "agentuse-" + secrets.token_hex(24)
        def body():
            yield (f'--{boundary}\r\nContent-Disposition: form-data; name="base_version_id"\r\n\r\n'
                   f'{base_version_id or ""}\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="skill.zip"\r\n'
                   'Content-Type: application/zip\r\n\r\n').encode()
            while True:
                chunk = stream.read(65536)
                if not chunk:
                    break
                yield chunk
            yield f'\r\n--{boundary}--\r\n'.encode()
        path = f"/api/skills/{_q(skill_id)}/versions" if skill_id else "/api/skills"
        with self._attachment_request("POST", path, user, body(), f"multipart/form-data; boundary={boundary}") as resp:
            return json.load(resp)

    def download_skill_package(self, user, skill_id, version_id):
        """Returns a response stream that the caller must close."""
        return self._attachment_request("GET", f"/api/skills/{_q(skill_id)}/versions/{_q(version_id)}/package", user)

    def upload_attachment(self, user, conversation_id, filename, stream) -> Attachment:
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
        with self._attachment_request("POST", f"/api/conversations/{_q(conversation_id)}/attachments", user,
                                      body(), f"multipart/form-data; boundary={boundary}") as resp:
            return json.load(resp)

    def list_attachments(self, user, conversation_id, limit=None, cursor=None) -> AttachmentPage:
        query = urllib.parse.urlencode(_drop_none(limit=limit, cursor=cursor))
        return self._request("GET", f"/api/conversations/{_q(conversation_id)}/attachments?{query}", user)

    def get_attachment(self, user, conversation_id, attachment_id) -> Attachment:
        return self._request("GET", f"/api/conversations/{_q(conversation_id)}/attachments/{_q(attachment_id)}", user)

    def download_attachment(self, user, conversation_id, attachment_id):
        """Return a response stream. Use it as a context manager or close it."""
        return self._attachment_request("GET", f"/api/conversations/{_q(conversation_id)}/attachments/{_q(attachment_id)}/content", user)

    def delete_attachment(self, user, conversation_id, attachment_id):
        self._request("DELETE", f"/api/conversations/{_q(conversation_id)}/attachments/{_q(attachment_id)}", user)

    # conversations

    def list_conversations(self, user):
        return self._request("GET", "/api/conversations", user)

    def create_conversation(self, user, prompt=None, title=None, to_agent_ids=None, to_user_ids=None):
        """创建共享会话；首条消息可指定 Agent 和用户收件人。"""
        return self._request(
            "POST", "/api/conversations", user, _drop_none(prompt=prompt, title=title, to_agent_ids=to_agent_ids, to_user_ids=to_user_ids)
        )

    def send_message(self, user, conversation_id, prompt=None, to_agent_ids=None, to_user_ids=None, attachment_ids=None, reply_to_message_id=None):
        """发送共享消息；返回 message、runs 和 deliveries。"""
        return self._request(
            "POST", f"/api/conversations/{_q(conversation_id)}/messages", user, _drop_none(prompt=prompt, to_agent_ids=to_agent_ids, to_user_ids=to_user_ids, attachment_ids=attachment_ids, reply_to_message_id=reply_to_message_id)
        )

    def get_conversation(self, user, conversation_id):
        """返回 {"conversation": …, "messages": […], "runs": […], "deliveries": […], "questions": […], "events": […]}。"""
        return self._request("GET", f"/api/conversations/{_q(conversation_id)}", user)

    def delete_conversation(self, user, conversation_id):
        self._request("DELETE", f"/api/conversations/{_q(conversation_id)}", user)

    def resolve_conversation(self, user, conversation_id):
        """把 idle 的会话标记为已完成;再发消息就重新打开。"""
        return self._request("POST", f"/api/conversations/{_q(conversation_id)}/resolve", user)

    def reopen_conversation(self, user, conversation_id):
        return self._request("POST", f"/api/conversations/{_q(conversation_id)}/reopen", user)

    def get_run(self, user, conversation_id, run_id) -> RunDetail:
        return self._request("GET", f"/api/conversations/{_q(conversation_id)}/runs/{_q(run_id)}", user)

    def cancel_run(self, user, conversation_id, run_id) -> Run:
        return self._request("POST", f"/api/conversations/{_q(conversation_id)}/runs/{_q(run_id)}/cancel", user)


    def reply_run_request(self, user, conversation_id, run_id, request_id, answer):
        return self._request("POST", f"/api/conversations/{_q(conversation_id)}/runs/{_q(run_id)}/requests/{_q(request_id)}/reply", user, {"answer": answer})

    def wait_run(self, user, conversation_id, run_id, interval=2, timeout=None, return_on_waiting=False) -> RunDetail:
        start = time.monotonic()
        while True:
            detail = self.get_run(user, conversation_id, run_id)
            if detail["run"]["status"] in ("completed", "failed", "cancelled", "rejected") or (return_on_waiting and detail["run"]["status"] == "waiting"):
                return detail
            if timeout is not None and time.monotonic() - start >= timeout:
                raise TimeoutError("agentuse: wait_run timed out")
            time.sleep(interval)

    def wait_message_runs(self, user, conversation_id, message_id, interval=2, timeout=None):
        start = time.monotonic()
        while True:
            detail = self.get_conversation(user, conversation_id)
            if all(d["status"] in ("completed", "failed", "cancelled", "rejected") for d in detail.get("deliveries", []) if d["message_id"] == message_id):
                return detail
            if timeout is not None and time.monotonic() - start >= timeout:
                raise TimeoutError("agentuse: wait_message_runs timed out")
            time.sleep(interval)

    # works:协调者 + 线程 + 共用说明

    def list_works(self, user):
        return self._request("GET", "/api/works", user)

    def create_work(self, user, name, agent_id, instructions=None):
        """建工作，同时建出协调者会话，返回 {"work": …, "coordinator": …}。给协调者发消息用 send_message。"""
        return self._request("POST", "/api/works", user, _drop_none(name=name, agent_id=agent_id, instructions=instructions))

    def get_work(self, user, work_id):
        return self._request("GET", f"/api/works/{_q(work_id)}", user)

    def update_work(self, user, work_id, name=None, instructions=None, coordinator_agent_id=None):
        return self._request("PATCH", f"/api/works/{_q(work_id)}", user, _drop_none(name=name, instructions=instructions, coordinator_agent_id=coordinator_agent_id))

    def delete_work(self, user, work_id):
        """连协调者和所有线程一起删。"""
        self._request("DELETE", f"/api/works/{_q(work_id)}", user)

    def list_work_conversations(self, user, work_id):
        """列工作的线程(不含协调者)。"""
        return self._request("GET", f"/api/conversations?work_id={_q(work_id)}", user)

    def wait_conversation(self, user, conversation_id, interval=2, timeout=None, return_on_waiting=False):
        """轮询直到会话回到 idle(或已完成)或在等回答提问(waiting),返回最后一次 get_conversation 的结果。超时抛 TimeoutError。"""
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            detail = self.get_conversation(user, conversation_id)
            if detail["conversation"]["status"] in ("idle", "resolved") or (detail["conversation"]["status"] == "waiting" and (return_on_waiting)):
                return detail
            if deadline is not None and time.monotonic() >= deadline:
                raise TimeoutError(f"agentuse: conversation {conversation_id} still {detail['conversation']['status']}")
            time.sleep(interval)


def _q(s):
    return urllib.parse.quote(str(s), safe="")


def _drop_none(**kw):
    return {k: v for k, v in kw.items() if v is not None}
