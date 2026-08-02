import json
import logging
import mimetypes
import os
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse

import grpc
from eve_proto import agent_pb2, agent_pb2_grpc


WEB_ROOT = Path(os.getenv("BEEMO_UI_WEB_ROOT", "/app/web/beemo")).resolve()
ORCH_ADDR = os.getenv("ORCH_ADDR", "eve-orchestrator:5013")
REQUEST_TIMEOUT_SECONDS = float(os.getenv("BEEMO_UI_TIMEOUT_SECONDS", "900"))


class BeemoUIHandler(BaseHTTPRequestHandler):
    server_version = "beemo-ui/0.1"

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/healthz":
            self.write_json({"ok": True})
            return
        if parsed.path == "/api/workspaces":
            try:
                self.write_json({"workspaces": list_workspaces()})
            except grpc.RpcError as exc:
                self.write_json({"error": exc.details() or exc.code().name}, HTTPStatus.BAD_GATEWAY)
            return
        if parsed.path == "/api/sessions":
            try:
                self.write_json({"sessions": list_sessions()})
            except grpc.RpcError as exc:
                self.write_json({"error": exc.details() or exc.code().name}, HTTPStatus.BAD_GATEWAY)
            return
        if parsed.path == "/api/session":
            session_id = str((parse_qs(parsed.query).get("id") or [""])[0]).strip()
            if not session_id:
                self.write_json({"error": "session id is required"}, HTTPStatus.BAD_REQUEST)
                return
            try:
                self.write_json(get_session(session_id))
            except grpc.RpcError as exc:
                self.write_json({"error": exc.details() or exc.code().name}, HTTPStatus.NOT_FOUND)
            return
        self.serve_static()

    def do_POST(self):
        if self.path not in {"/api/chat", "/api/agent", "/api/approve", "/api/diff"}:
            self.send_error(HTTPStatus.NOT_FOUND)
            return

        try:
            payload = self.read_json()
            if self.path == "/api/diff":
                response = get_diff(
                    str(payload.get("session_id") or "").strip(),
                    str(payload.get("workspace") or "").strip(),
                )
                self.write_json({"diff": response.diff, "error": response.error})
                return
            if self.path == "/api/approve":
                response = approve(
                    str(payload.get("session_id") or "").strip(),
                    str(payload.get("approval_id") or "").strip(),
                    bool(payload.get("approved")),
                )
                self.write_json({"accepted": response.accepted, "status": response.status})
                return

            session_id = str(payload.get("session_id") or "web").strip() or "web"
            raw_messages = payload.get("messages") or []
            messages = [
                agent_pb2.ChatMessage(
                    role=str(item.get("role") or "").strip(),
                    content=str(item.get("content") or "").strip(),
                )
                for item in raw_messages
                if str(item.get("role") or "").strip() and str(item.get("content") or "").strip()
            ]
            if not messages:
                self.write_json({"error": "message is required"}, HTTPStatus.BAD_REQUEST)
                return

            options = {
                str(key): str(value)
                for key, value in (payload.get("options") or {}).items()
                if str(key).strip() and str(value).strip()
            }
            if self.path == "/api/agent":
                self.stream_agent(
                    session_id,
                    messages,
                    str(payload.get("mode") or "chat").strip(),
                    str(payload.get("workspace") or "").strip(),
                    options,
                )
                return
            response = chat(session_id, messages, options)
            self.write_json({"text": response.text})
        except grpc.RpcError as exc:
            logging.exception("orchestrator request failed")
            detail = exc.details() or exc.code().name
            self.write_json({"error": f"orchestrator request failed: {detail}"}, HTTPStatus.BAD_GATEWAY)
        except Exception as exc:
            logging.exception("chat request failed")
            self.write_json({"error": str(exc)}, HTTPStatus.INTERNAL_SERVER_ERROR)

    def stream_agent(self, session_id, messages, mode, workspace, options):
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", "application/x-ndjson; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Accel-Buffering", "no")
        self.end_headers()
        try:
            for event in run_agent(session_id, messages, mode, workspace, options):
                item = {
                    "session_id": event.session_id,
                    "type": event.type,
                    "text": event.text,
                    "tool": event.tool,
                    "payload_json": event.payload_json,
                    "timestamp_unix_ms": event.timestamp_unix_ms,
                    "approval_id": event.approval_id,
                }
                self.wfile.write(json.dumps(item).encode("utf-8") + b"\n")
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            logging.info("agent stream disconnected session=%s", session_id)
        except grpc.RpcError as exc:
            item = {"type": "error", "text": exc.details() or exc.code().name}
            self.wfile.write(json.dumps(item).encode("utf-8") + b"\n")
            self.wfile.flush()

    def serve_static(self):
        path = unquote(self.path.split("?", 1)[0])
        if path == "/":
            path = "/index.html"
        relative = path.lstrip("/")
        target = (WEB_ROOT / relative).resolve()
        if not str(target).startswith(str(WEB_ROOT)) or not target.is_file():
            self.send_error(HTTPStatus.NOT_FOUND)
            return

        content_type = mimetypes.guess_type(target.name)[0] or "application/octet-stream"
        data = target.read_bytes()
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(data)

    def read_json(self):
        length = int(self.headers.get("Content-Length") or "0")
        if length <= 0:
            return {}
        return json.loads(self.rfile.read(length).decode("utf-8"))

    def write_json(self, payload, status=HTTPStatus.OK):
        data = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):
        logging.info("ui.http %s", fmt % args)


def chat(session_id, messages, options=None):
    with grpc.insecure_channel(ORCH_ADDR) as channel:
        client = agent_pb2_grpc.OrchestratorStub(channel)
        return client.Chat(
            agent_pb2.ChatRequest(
                session_id=session_id,
                messages=messages,
                options=options or {},
            ),
            timeout=REQUEST_TIMEOUT_SECONDS,
        )


def run_agent(session_id, messages, mode, workspace, options=None):
    channel = grpc.insecure_channel(ORCH_ADDR)
    client = agent_pb2_grpc.OrchestratorStub(channel)
    try:
        yield from client.RunAgent(
            agent_pb2.AgentRequest(
                session_id=session_id,
                messages=messages,
                mode=mode,
                workspace=workspace,
                options=options or {},
            ),
            timeout=REQUEST_TIMEOUT_SECONDS,
        )
    finally:
        channel.close()


def approve(session_id, approval_id, approved):
    with grpc.insecure_channel(ORCH_ADDR) as channel:
        client = agent_pb2_grpc.OrchestratorStub(channel)
        return client.Approve(
            agent_pb2.ApprovalDecision(
                session_id=session_id,
                approval_id=approval_id,
                approved=approved,
            ),
            timeout=10,
        )


def list_workspaces():
    with grpc.insecure_channel(ORCH_ADDR) as channel:
        client = agent_pb2_grpc.OrchestratorStub(channel)
        response = client.ListAgentWorkspaces(agent_pb2.ListWorkspacesRequest(), timeout=10)
        return list(response.roots)


def list_sessions():
    with grpc.insecure_channel(ORCH_ADDR) as channel:
        client = agent_pb2_grpc.OrchestratorStub(channel)
        response = client.ListAgentSessions(agent_pb2.SessionListRequest(limit=30), timeout=10)
        return [
            {
                "session_id": item.session_id,
                "mode": item.mode,
                "workspace": item.workspace,
                "status": item.status,
                "updated_unix_ms": item.updated_unix_ms,
            }
            for item in response.sessions
        ]


def get_session(session_id):
    with grpc.insecure_channel(ORCH_ADDR) as channel:
        client = agent_pb2_grpc.OrchestratorStub(channel)
        response = client.GetAgentSession(agent_pb2.SessionRequest(session_id=session_id), timeout=10)
        return {
            "session": {
                "session_id": response.session.session_id,
                "mode": response.session.mode,
                "workspace": response.session.workspace,
                "status": response.session.status,
                "updated_unix_ms": response.session.updated_unix_ms,
            },
            "events": [
                {
                    "type": event.type,
                    "text": event.text,
                    "tool": event.tool,
                    "payload_json": event.payload_json,
                    "timestamp_unix_ms": event.timestamp_unix_ms,
                }
                for event in response.events
            ],
        }


def get_diff(session_id, workspace):
    with grpc.insecure_channel(ORCH_ADDR) as channel:
        client = agent_pb2_grpc.OrchestratorStub(channel)
        return client.GetAgentDiff(
            agent_pb2.AgentDiffRequest(session_id=session_id, workspace=workspace),
            timeout=30,
        )


def main():
    logging.basicConfig(
        level=os.getenv("LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)s %(message)s",
    )
    host = os.getenv("BEEMO_UI_HOST", "0.0.0.0")
    port = int(os.getenv("BEEMO_UI_PORT", "5017"))
    server = ThreadingHTTPServer((host, port), BeemoUIHandler)
    logging.info("beemo.ui listening=%s:%d web_root=%s orchestrator=%s", host, port, WEB_ROOT, ORCH_ADDR)
    server.serve_forever()


if __name__ == "__main__":
    main()
