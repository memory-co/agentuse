#!/usr/bin/env python3
"""Server-owned Work API tool. Requires Python 3.9+ and the run's context command."""
import argparse
import json
import mimetypes
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class ServerError(ValueError):
    def __init__(self, code, message):
        super().__init__("Server returned %s: %s" % (code, message))
        self.code = code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    for name in ("agents", "threads"):
        commands.add_parser(name)
    for name in ("new", "send"):
        command = commands.add_parser(name)
        if name == "send":
            command.add_argument("--attachments", default="")
        command.add_argument("--agents", default="")
        command.add_argument("--users", default="")
        if name == "new":
            command.add_argument("--defer-start", action="store_true")
            command.add_argument("prompt", nargs="?")
        else:
            command.add_argument("thread")
            command.add_argument("prompt")
    for name in ("read", "resolve", "upload"):
        command = commands.add_parser(name)
        command.add_argument("thread")
        if name == "upload":
            command.add_argument("file", type=Path)
    args = parser.parse_args()
    if args.action == "new" and ((args.defer_start and (args.prompt is not None or args.agents or args.users)) or (not args.defer_start and args.prompt is None)):
        parser.error("new requires a message, or --defer-start without recipients")
    base, device_key = os.environ.get("AGENTUSE_SERVER", ""), os.environ.get("AGENTUSE_TOKEN", "")
    if not base or not device_key:
        raise ValueError("Run environment is missing Server identity")
    opener = urllib.request.build_opener(NoRedirect)

    def request(token, method, path, value=None, raw=None, content_type=None):
        headers = {"Authorization": "Bearer " + token, "Accept": "application/json"}
        body = json.dumps(value).encode() if value is not None else raw
        if body is not None:
            headers["Content-Type"] = content_type or "application/json"
        req = urllib.request.Request(base.rstrip("/") + path, data=body, headers=headers, method=method)
        try:
            with opener.open(req, timeout=60) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            try:
                message = json.load(error).get("error", error.reason)
            except (ValueError, AttributeError):
                message = error.reason
            raise ServerError(error.code, message) from None

    context = json.loads(subprocess.check_output(["context", "status"], text=True))
    identity = {
        "conversation_id": context["conversation_id"],
        "agent_id": context["agent_id"], "run_id": context["run_id"],
    }
    # Native acceptance can precede the Client's report to Server. Only retry
    # authorization while that association catches up; never replay Work writes.
    deadline = time.monotonic() + 2
    while True:
        try:
            authorization = request(device_key, "POST", "/api/agent-tools/work/authorize", identity)
            break
        except ServerError as error:
            if error.code != 403 or time.monotonic() >= deadline:
                raise
            time.sleep(0.05)
    token = authorization["token"]
    targets = lambda value: [part.strip() for part in value.split(",") if part.strip()]
    thread_path = "/api/conversations/" + urllib.parse.quote(getattr(args, "thread", ""), safe="")
    if args.action == "agents":
        result = request(token, "GET", "/api/agents")
    elif args.action == "threads":
        result = request(token, "GET", "/api/conversations")
    elif args.action in ("new", "send"):
        value = {} if args.action == "new" and args.defer_start else {
            "prompt": args.prompt, "to_agent_ids": targets(args.agents), "to_user_ids": targets(args.users)}
        if args.action == "send":
            value["attachment_ids"] = targets(args.attachments)
        path = "/api/conversations" if args.action == "new" else thread_path + "/messages"
        result = request(token, "POST", path, value)
    elif args.action == "read":
        result = request(token, "GET", thread_path)
    elif args.action == "resolve":
        result = request(token, "POST", thread_path + "/resolve")
    else:
        if not args.file.is_file():
            raise ValueError("Upload requires a regular file")
        boundary = "agentuse-" + uuid.uuid4().hex
        filename = args.file.name
        if any(c in filename for c in '\r\n"\\'):
            raise ValueError("Invalid upload filename")
        mime = mimetypes.guess_type(filename)[0] or "application/octet-stream"
        head = ('--%s\r\nContent-Disposition: form-data; name="file"; filename="%s"\r\nContent-Type: %s\r\n\r\n' % (boundary, filename, mime)).encode()
        body = head + args.file.read_bytes() + ("\r\n--%s--\r\n" % boundary).encode()
        result = request(token, "POST", thread_path + "/attachments", raw=body, content_type="multipart/form-data; boundary=" + boundary)
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print("work tool: " + str(error), file=sys.stderr)
        sys.exit(1)
