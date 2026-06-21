#!/usr/bin/env python3
import argparse
import json
import os
import queue
import re
import subprocess
import sys
import threading
import time
import tkinter as tk
from datetime import datetime, timezone
from pathlib import Path


ROOT_DIR = Path(__file__).resolve().parents[1]
DEFAULT_START_CMD = "./scripts/beemo-start.sh vllm-cpu"

EXPRESSIONS = {
    "neutral",
    "happy",
    "excited",
    "curious",
    "thinking",
    "concerned",
    "sad",
    "surprised",
    "apologetic",
    "sleepy",
    "error",
}

TAG_PATTERN = re.compile(r"^\s*\[(?:emotion|expression|face):\s*([a-z_-]+)\]\s*", re.I)
ISO_TIMESTAMP_RE = re.compile(r"^\s*(?:time=|timestamp=)?\[?(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z?)\]?\s*$")


def main() -> int:
    args = parse_args()
    if args.help_only:
        return 0

    app = BeemoApp(args)
    app.run()
    return 0


def parse_args():
    parser = argparse.ArgumentParser(description="Launch Beemo as a native fullscreen Linux app.")
    parser.add_argument("--windowed", action="store_true", help="open in a normal window")
    parser.add_argument("--no-start", action="store_true", help="do not start Beemo services first")
    parser.add_argument(
        "--start-cmd",
        default=os.getenv("BEEMO_NATIVE_START_CMD", DEFAULT_START_CMD),
        help=f"service startup command, default: {DEFAULT_START_CMD!r}",
    )
    parser.add_argument("--help-only", action="store_true", help=argparse.SUPPRESS)
    return parser.parse_args()


class BeemoApp:
    def __init__(self, args):
        self.args = args
        self.messages = []
        self.visible_messages = [{"role": "assistant", "content": "Hi. I am starting up."}]
        self.events = queue.Queue()
        self.busy = False
        self.voice_ready = False
        self.voice_pulse = False
        self.wake_subscription_started = False
        self.last_voice_prompt = ""
        self.expression = "neutral"
        self.session_id = os.getenv("BEEMO_NATIVE_SESSION_ID", os.getenv("WAKEWORD_SESSION_ID", "voice-loop"))

        self.root = tk.Tk()
        self.root.title("Beemo")
        self.root.configure(bg="#111413")
        self.root.minsize(900, 620)
        if not args.windowed:
            self.root.attributes("-fullscreen", True)

        self.root.bind("<Escape>", self.exit_fullscreen)
        self.root.bind("<F11>", self.toggle_fullscreen)

        self.build_ui()
        self.set_expression("neutral")
        self.render_messages()
        self.root.after(100, self.poll_events)
        self.root.after(250, self.poll_voice_services)
        self.root.after(500, self.pulse_voice_icon)

        if not args.no_start:
            self.start_background("start", self.start_beemo)
        else:
            self.set_status("ready")
            self.show_message("assistant", "Hi. I am listening.")

    def build_ui(self):
        self.root.grid_rowconfigure(0, weight=0, minsize=250)
        self.root.grid_rowconfigure(1, weight=1)
        self.root.grid_rowconfigure(2, weight=0)
        self.root.grid_columnconfigure(0, weight=1)

        self.face = tk.Canvas(self.root, bg="#111413", highlightthickness=0, height=270)
        self.face.grid(row=0, column=0, sticky="nsew", padx=20, pady=(18, 6))

        status_frame = tk.Frame(self.face, bg="#111413")
        self.face.create_window(0, 0, window=status_frame, anchor="nw", tags=("status_window",))
        self.expression_var = tk.StringVar(value="neutral")
        self.status_var = tk.StringVar(value="idle")
        self.tool_var = tk.StringVar(value="beemo")
        badge_style = {
            "bg": "#1b2220",
            "fg": "#9fb4ad",
            "padx": 10,
            "pady": 5,
            "font": ("TkDefaultFont", 10),
        }
        tk.Label(status_frame, textvariable=self.expression_var, **badge_style).pack(side="left", padx=5)
        tk.Label(status_frame, textvariable=self.status_var, **badge_style).pack(side="left", padx=5)
        tool_frame = tk.Frame(self.face, bg="#111413")
        self.face.create_window(0, 0, window=tool_frame, anchor="ne", tags=("tool_window",))
        tk.Label(tool_frame, textvariable=self.tool_var, **badge_style).pack(side="left", padx=5)

        self.stage = tk.Frame(self.root, bg="#1b2220", highlightbackground="#40504b", highlightthickness=1)
        self.stage.grid(row=1, column=0, sticky="nsew", padx=48, pady=(0, 18))
        self.stage.grid_rowconfigure(0, weight=1)
        self.stage.grid_rowconfigure(1, weight=2)
        self.stage.grid_columnconfigure(0, weight=1)

        self.previous_label = tk.Label(
            self.stage,
            bg="#1b2220",
            fg="#9fb4ad",
            wraplength=760,
            justify="center",
            font=("TkDefaultFont", 15),
        )
        self.previous_label.grid(row=0, column=0, sticky="s", padx=32, pady=(18, 24))

        self.current_label = tk.Label(
            self.stage,
            bg="#1b2220",
            fg="#72f2c8",
            wraplength=840,
            justify="center",
            font=("TkDefaultFont", 26),
        )
        self.current_label.grid(row=1, column=0, sticky="n", padx=32, pady=(12, 24))

        input_frame = tk.Frame(self.root, bg="#111413", highlightbackground="#40504b", highlightthickness=1)
        input_frame.grid(row=2, column=0, sticky="ew", padx=0, pady=0)
        input_frame.grid_columnconfigure(1, weight=1)

        self.voice_button = tk.Button(
            input_frame,
            text="●",
            command=self.voice_status_message,
            bg="#202b28",
            fg="#727b77",
            activebackground="#263631",
            activeforeground="#72f2c8",
            relief="flat",
            width=6,
            height=2,
            font=("TkDefaultFont", 18, "bold"),
        )
        self.voice_button.grid(row=0, column=0, sticky="ns", padx=(24, 10), pady=14)

        input_wrap = tk.Frame(input_frame, bg="#111413")
        input_wrap.grid(row=0, column=1, sticky="ew", pady=14)
        input_wrap.grid_columnconfigure(0, weight=1)

        self.input_text = tk.Text(
            input_wrap,
            height=2,
            wrap="word",
            bg="#202b28",
            fg="#edf7f2",
            insertbackground="#edf7f2",
            relief="flat",
            padx=12,
            pady=9,
            font=("TkDefaultFont", 14),
        )
        self.input_text.grid(row=0, column=0, sticky="ew")
        scrollbar = tk.Scrollbar(input_wrap, orient="vertical", command=self.input_text.yview)
        scrollbar.grid(row=0, column=1, sticky="ns")
        self.input_text.configure(yscrollcommand=scrollbar.set)
        self.input_text.bind("<Return>", self.submit_from_key)

        self.send_button = tk.Button(
            input_frame,
            text="Send",
            command=self.submit,
            bg="#236e5d",
            fg="#edf7f2",
            activebackground="#2d967c",
            activeforeground="#edf7f2",
            relief="flat",
            width=9,
            height=2,
        )
        self.send_button.grid(row=0, column=2, sticky="ns", padx=(10, 24), pady=14)

        self.root.bind("<Configure>", self.on_resize)

    def run(self):
        self.root.mainloop()

    def on_resize(self, _event=None):
        width = max(self.root.winfo_width(), 1)
        self.previous_label.configure(wraplength=max(380, int(width * 0.72)))
        self.current_label.configure(wraplength=max(420, int(width * 0.78)))
        self.draw_face()

    def draw_face(self):
        self.face.delete("head")
        width = max(self.face.winfo_width(), 1)
        height = max(self.face.winfo_height(), 1)
        size = min(width * 0.34, height * 0.82, 330)
        x0 = (width - size) / 2
        y0 = (height - size * 0.82) / 2 + 8
        x1 = x0 + size
        y1 = y0 + size * 0.82

        self.face.coords("status_window", width / 2 - 80, height - 36)
        self.face.coords("tool_window", width - 18, height - 36)
        self.face.create_line(width / 2, y0 - 30, width / 2 - 8, y0, fill="#72f2c8", width=4, tags="head")
        self.face.create_oval(width / 2 - 10, y0 - 42, width / 2 + 4, y0 - 28, fill="#f7c66f", outline="", tags="head")
        self.face.create_rectangle(x0, y0, x1, y1, fill="#3ab996", outline="#273632", width=3, tags="head")

        sx0 = x0 + size * 0.12
        sy0 = y0 + size * 0.14
        sx1 = x1 - size * 0.12
        sy1 = y1 - size * 0.15
        self.face.create_rectangle(sx0, sy0, sx1, sy1, fill="#d7fff0", outline="#182722", width=3, tags="head")

        cx = (sx0 + sx1) / 2
        eye_y = sy0 + (sy1 - sy0) * 0.43
        gap = size * 0.17
        eye_size = size * 0.09
        left = (cx - gap, eye_y)
        right = (cx + gap, eye_y)
        self.draw_eye(left, eye_size, "left")
        self.draw_eye(right, eye_size, "right")
        self.draw_mouth(cx, sy0 + (sy1 - sy0) * 0.72, size)

    def draw_eye(self, center, size, side):
        x, y = center
        expr = self.expression
        color = "#21443c"
        if expr == "error":
            color = "#ff7c7c"
            self.face.create_line(x - size, y - size, x + size, y + size, fill=color, width=7, tags="head")
            self.face.create_line(x + size, y - size, x - size, y + size, fill=color, width=7, tags="head")
            return
        if expr in {"happy", "excited", "sad", "apologetic", "sleepy"} or (expr == "thinking" and side == "left"):
            self.face.create_line(x - size, y, x + size, y, fill=color, width=7, capstyle="round", tags="head")
            return
        if expr == "curious" and side == "right":
            size *= 1.25
        self.face.create_oval(x - size, y - size, x + size, y + size, fill=color, outline="", tags="head")

    def draw_mouth(self, cx, cy, size):
        expr = self.expression
        color = "#21443c"
        w = size * 0.14
        h = size * 0.07
        if expr == "error":
            self.face.create_line(cx - w, cy, cx + w, cy, fill="#ff7c7c", width=8, capstyle="round", tags="head")
        elif expr == "surprised":
            self.face.create_oval(cx - h, cy - h, cx + h, cy + h, outline=color, width=6, tags="head")
        elif expr in {"concerned", "sad", "apologetic"}:
            self.face.create_arc(cx - w, cy - h / 2, cx + w, cy + h * 1.8, start=25, extent=130, style="arc", outline=color, width=6, tags="head")
        elif expr in {"happy", "excited"}:
            self.face.create_arc(cx - w, cy - h, cx + w, cy + h, start=205, extent=130, style="arc", outline=color, width=6, tags="head")
        else:
            self.face.create_line(cx - w, cy, cx + w, cy, fill=color, width=6, capstyle="round", tags="head")

    def set_expression(self, expression):
        self.expression = expression if expression in EXPRESSIONS else "neutral"
        self.expression_var.set(self.expression)
        self.draw_face()

    def set_status(self, status):
        self.status_var.set(status)

    def set_tool(self, tools):
        self.tool_var.set(tool_label(tools))

    def show_message(self, role, content):
        self.visible_messages.append({"role": role, "content": content})
        while len(self.visible_messages) > 2:
            self.visible_messages.pop(0)
        self.render_messages()

    def render_messages(self):
        previous = self.visible_messages[0] if len(self.visible_messages) > 1 else None
        current = self.visible_messages[-1]
        self.previous_label.configure(text=previous["content"] if previous else "")
        self.current_label.configure(text=current["content"])
        self.previous_label.configure(fg=self.color_for(previous["role"], faded=True) if previous else "#9fb4ad")
        self.current_label.configure(fg=self.color_for(current["role"], faded=False))

    def color_for(self, role, faded):
        if role == "user":
            return "#ad8b4d" if faded else "#f7c66f"
        return "#5fa88f" if faded else "#72f2c8"

    def submit_from_key(self, event):
        if event.state & 0x1:
            return None
        self.submit()
        return "break"

    def submit(self):
        if self.busy:
            return
        text = self.input_text.get("1.0", "end").strip()
        if not text:
            return
        self.input_text.delete("1.0", "end")
        self.messages.append({"role": "user", "content": text})
        self.show_message("user", text)
        self.set_expression("thinking")
        self.set_status("thinking")
        self.set_tool([])
        self.busy = True
        self.start_background("chat", lambda: self.chat(text))

    def chat(self, _text):
        payload = {"session_id": self.session_id, "messages": self.messages}
        result = run_chat(payload)
        reply = result.get("text", "").strip() or "(empty response)"
        self.messages.append({"role": "assistant", "content": reply})
        self.events.put(("reply", {
            "text": reply,
            "tools": result.get("tools", []),
            "status": result.get("status", ""),
            "error_kind": result.get("errorKind", "") or result.get("error_kind", ""),
        }))

    def start_beemo(self):
        self.events.put(("status", "starting"))
        if not orchestrator_running():
            subprocess.run(self.args.start_cmd, cwd=ROOT_DIR, shell=True, check=True)
        self.events.put(("status", "ready"))
        self.events.put(("message", ("assistant", "Hi. I am listening.")))
        self.events.put(("start_wake_stream", None))

    def start_background(self, name, target):
        def wrapped():
            try:
                target()
            except Exception as exc:
                self.events.put(("error", f"{name} failed: {exc}"))

        thread = threading.Thread(target=wrapped, daemon=True)
        thread.start()

    def poll_events(self):
        while True:
            try:
                event, payload = self.events.get_nowait()
            except queue.Empty:
                break
            if event == "status":
                self.set_status(payload)
            elif event == "message":
                role, content = payload
                self.show_message(role, content)
            elif event == "reply":
                self.busy = False
                reply = payload.get("text", "")
                status = payload.get("status", "")
                error_kind = payload.get("error_kind", "")
                self.set_expression(expression_for_status(status, reply))
                self.show_message("assistant", clean_reply(reply))
                self.set_tool(payload.get("tools", []))
                self.set_status(status_label(status, error_kind))
            elif event == "voice_status":
                self.set_voice_ready(payload)
            elif event == "wake_event":
                self.handle_wake_event(payload)
            elif event == "start_wake_stream":
                self.ensure_wake_subscription()
            elif event == "error":
                self.busy = False
                self.set_expression("error")
                self.set_status("error")
                self.show_message("assistant", payload)
        self.root.after(100, self.poll_events)

    def poll_voice_services(self):
        ready = service_running("eve-wakeword") and service_running("eve-asr")
        self.events.put(("voice_status", ready))
        if ready:
            self.events.put(("start_wake_stream", None))
        self.root.after(2500, self.poll_voice_services)

    def set_voice_ready(self, ready):
        if self.voice_ready == ready:
            return
        self.voice_ready = ready
        if ready:
            self.voice_button.configure(state="normal", fg="#72f2c8", activeforeground="#72f2c8")
        else:
            self.voice_button.configure(state="normal", bg="#202b28", fg="#727b77", activeforeground="#727b77")

    def pulse_voice_icon(self):
        if self.voice_ready:
            self.voice_pulse = not self.voice_pulse
            self.voice_button.configure(bg="#1f5f50" if self.voice_pulse else "#263631", fg="#72f2c8")
        else:
            self.voice_button.configure(bg="#202b28", fg="#727b77")
        self.root.after(650, self.pulse_voice_icon)

    def ensure_wake_subscription(self):
        if self.wake_subscription_started or not self.voice_ready:
            return
        self.wake_subscription_started = True
        self.start_background("wake stream", self.watch_wake_stream)

    def watch_wake_stream(self):
        try:
            for event in stream_wake_events(self.session_id):
                self.events.put(("wake_event", event))
        finally:
            self.wake_subscription_started = False

    def handle_wake_event(self, payload):
        source = payload.get("source", "wakeword")
        prompt = (payload.get("prompt") or "").strip()
        response = (payload.get("response") or "").strip()
        transcript = (payload.get("transcript") or "").strip()
        tools = payload.get("tools", [])
        status = (payload.get("status") or "").strip()
        error_kind = (payload.get("errorKind") or payload.get("error_kind") or "").strip()

        if prompt:
            if prompt != self.last_voice_prompt:
                self.messages.append({"role": "user", "content": prompt})
                self.show_message("user", prompt)
                self.last_voice_prompt = prompt
            if not response:
                self.set_expression("thinking")
                self.set_status("thinking")
                return
        elif transcript:
            if transcript != self.last_voice_prompt:
                self.show_message("user", transcript)
                self.last_voice_prompt = transcript
            if not response:
                self.set_expression("thinking")
                self.set_status("thinking")
                return
        else:
            self.set_status("heard")
            self.set_expression("curious")
            return

        if response:
            self.messages.append({"role": "assistant", "content": response})
            self.set_expression(expression_for_status(status, response))
            self.show_message("assistant", clean_reply(response))
            self.set_tool(tools)
            self.set_status(status_label(status, error_kind))
        else:
            self.set_status(source)

    def voice_status_message(self):
        if self.voice_ready:
            self.set_expression("curious")
            self.show_message("assistant", "Voice is online. Say hey Beemo and I will follow it here.")
        else:
            self.set_expression("concerned")
            self.show_message("assistant", "Voice is offline. Start eve-wakeword and eve-asr to enable listening.")

    def exit_fullscreen(self, _event=None):
        self.root.attributes("-fullscreen", False)

    def toggle_fullscreen(self, _event=None):
        current = bool(self.root.attributes("-fullscreen"))
        self.root.attributes("-fullscreen", not current)


def orchestrator_running():
    proc = subprocess.run(
        ["docker", "ps", "--filter", "name=^/eve-orchestrator$", "--filter", "status=running", "--format", "{{.Names}}"],
        cwd=ROOT_DIR,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    return "eve-orchestrator" in proc.stdout.splitlines()


def service_running(name):
    proc = subprocess.run(
        ["docker", "ps", "--filter", f"name=^/{name}$", "--filter", "status=running", "--format", "{{.Names}}"],
        cwd=ROOT_DIR,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        check=False,
    )
    return name in proc.stdout.splitlines()


def run_chat(payload):
    payload_text = json.dumps(payload)
    proc = subprocess.run(
        [
            "docker",
            "exec",
            "-i",
            "eve-orchestrator",
            "grpcurl",
            "-plaintext",
            "-import-path",
            "/workspace",
            "-proto",
            "proto/agent.proto",
            "-d",
            payload_text,
            "localhost:5013",
            "eve.Orchestrator/Chat",
        ],
        cwd=ROOT_DIR,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if proc.returncode != 0:
        raise RuntimeError(proc.stderr.strip() or proc.stdout.strip() or "orchestrator request failed")
    return json.loads(proc.stdout)


def stream_wake_events(session_id):
    payload_text = json.dumps({"session_id": session_id})
    proc = subprocess.Popen(
        [
            "docker",
            "exec",
            "eve-orchestrator",
            "grpcurl",
            "-plaintext",
            "-import-path",
            "/workspace",
            "-proto",
            "proto/agent.proto",
            "-d",
            payload_text,
            "eve-wakeword:5020",
            "eve.WakeWord/StreamWake",
        ],
        cwd=ROOT_DIR,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    assert proc.stdout is not None
    try:
        yield from iter_json_stream(proc.stdout)
    finally:
        if proc.poll() is None:
            proc.terminate()


def iter_json_stream(stream):
    decoder = json.JSONDecoder()
    buffer = ""
    for line in stream:
        buffer += line
        while buffer.strip():
            stripped = buffer.lstrip()
            trim = len(buffer) - len(stripped)
            try:
                item, end = decoder.raw_decode(stripped)
            except json.JSONDecodeError:
                break
            yield item
            buffer = stripped[end:].lstrip()
            if trim and not buffer:
                break


def tool_label(tools):
    if not tools:
        return "beemo"
    labels = []
    for tool in tools:
        name = str(tool).strip()
        if not name:
            continue
        if name == "beemo.direct":
            labels.append("beemo")
        elif name == "older_sister":
            labels.append("big-sister")
        elif name == "get_time":
            labels.append("time")
        else:
            labels.append(name.replace("_", "-"))
    return ", ".join(labels) if labels else "beemo"


def expression_from_reply(reply):
    match = TAG_PATTERN.match(reply)
    if match:
        tagged = match.group(1).lower().replace("_", "-")
        if tagged in EXPRESSIONS:
            return tagged

    text = reply.lower()
    if any(needle in text for needle in ["request failed", "error", "failed", "can't connect", "cannot connect"]):
        return "error"
    if any(needle in text for needle in ["sorry", "apologize", "my mistake", "i was wrong"]):
        return "apologetic"
    if any(needle in text for needle in ["i'm not sure", "i do not know", "i don't know", "missing", "need more", "clarify"]):
        return "concerned"
    if any(needle in text for needle in ["?", "wonder", "curious", "what detail", "who is this about"]):
        return "curious"
    if any(needle in text for needle in ["let me think", "thinking", "consider", "reason"]):
        return "thinking"
    if any(needle in text for needle in ["great", "nice", "glad", "happy", "that works", "done"]):
        return "happy"
    if any(needle in text for needle in ["wow", "amazing", "excited", "awesome", "excellent"]):
        return "excited"
    if any(needle in text for needle in ["oh", "surprise", "unexpected"]):
        return "surprised"
    if any(needle in text for needle in ["sad", "hurt", "upset", "unfortunately"]):
        return "sad"
    if any(needle in text for needle in ["tired", "sleepy", "rest"]):
        return "sleepy"
    return "neutral"


def expression_for_status(status, reply):
    normalized = (status or "").strip().lower()
    if normalized == "error":
        return "error"
    if normalized == "needs_input":
        return "curious"
    return expression_from_reply(reply)


def status_label(status, error_kind):
    normalized = (status or "").strip().lower()
    kind = (error_kind or "").strip().replace("_", "-")
    if normalized == "error":
        return kind or "error"
    if normalized == "needs_input":
        return kind or "needs-input"
    return "ready"


def clean_reply(reply):
    text = TAG_PATTERN.sub("", reply).strip()
    return format_iso_timestamp_reply(text) or text


def format_iso_timestamp_reply(text):
    match = ISO_TIMESTAMP_RE.match(text)
    if not match:
        return ""
    raw = match.group(1)
    if raw.endswith("Z"):
        raw = raw[:-1] + "+00:00"
    try:
        timestamp = datetime.fromisoformat(raw)
    except ValueError:
        return ""
    if timestamp.tzinfo is None:
        timestamp = timestamp.replace(tzinfo=timezone.utc)
    local = timestamp.astimezone()
    return f"It is {local.strftime('%-I:%M %p on %B %-d, %Y')}."


if __name__ == "__main__":
    raise SystemExit(main())
