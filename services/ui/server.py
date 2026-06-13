import json
import logging
import mimetypes
import os
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import unquote

import grpc
from eve_proto import agent_pb2, agent_pb2_grpc


WEB_ROOT = Path(os.getenv("BEEMO_UI_WEB_ROOT", "/app/web/beemo")).resolve()
ORCH_ADDR = os.getenv("ORCH_ADDR", "eve-orchestrator:5013")
REQUEST_TIMEOUT_SECONDS = float(os.getenv("BEEMO_UI_TIMEOUT_SECONDS", "120"))


class BeemoUIHandler(BaseHTTPRequestHandler):
    server_version = "beemo-ui/0.1"

    def do_GET(self):
        if self.path == "/healthz":
            self.write_json({"ok": True})
            return
        self.serve_static()

    def do_POST(self):
        if self.path != "/api/chat":
            self.send_error(HTTPStatus.NOT_FOUND)
            return

        try:
            payload = self.read_json()
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

            response = chat(session_id, messages)
            self.write_json({"text": response.text})
        except grpc.RpcError as exc:
            logging.exception("orchestrator request failed")
            detail = exc.details() or exc.code().name
            self.write_json({"error": f"orchestrator request failed: {detail}"}, HTTPStatus.BAD_GATEWAY)
        except Exception as exc:
            logging.exception("chat request failed")
            self.write_json({"error": str(exc)}, HTTPStatus.INTERNAL_SERVER_ERROR)

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


def chat(session_id, messages):
    with grpc.insecure_channel(ORCH_ADDR) as channel:
        client = agent_pb2_grpc.OrchestratorStub(channel)
        return client.Chat(
            agent_pb2.ChatRequest(session_id=session_id, messages=messages),
            timeout=REQUEST_TIMEOUT_SECONDS,
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
