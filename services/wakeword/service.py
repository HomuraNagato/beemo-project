import logging
import os
import queue
import signal
import subprocess
import threading
import time
from collections import deque
from concurrent import futures
from typing import Optional, Set

import grpc
import numpy as np
from eve_proto import agent_pb2, agent_pb2_grpc
from openwakeword.model import Model as OpenWakeWordModel

from services.wakeword.logic import extract_prompt, should_listen_for_followup, split_phrases


def env_int(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    return int(raw)


def env_float(name: str, default: float) -> float:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    return float(raw)


def env_bool(name: str, default: bool) -> bool:
    raw = os.getenv(name)
    if raw is None or raw.strip() == "":
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def pcm16_rms(frame: bytes) -> int:
    samples = np.frombuffer(frame, dtype=np.int16)
    if samples.size == 0:
        return 0
    return int(np.sqrt(np.mean(np.square(samples.astype(np.float32)))))


class EventBroker:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._subscribers: Set[queue.Queue] = set()

    def subscribe(self) -> queue.Queue:
        q: queue.Queue = queue.Queue()
        with self._lock:
            self._subscribers.add(q)
        return q

    def unsubscribe(self, q: queue.Queue) -> None:
        with self._lock:
            self._subscribers.discard(q)

    def publish(self, event: agent_pb2.WakeDetected) -> None:
        with self._lock:
            subscribers = list(self._subscribers)
        for subscriber in subscribers:
            subscriber.put(event)


class WakeWordServicer(agent_pb2_grpc.WakeWordServicer):
    def __init__(self, broker: EventBroker) -> None:
        self.broker = broker

    def StreamWake(self, request, context):
        subscriber = self.broker.subscribe()
        try:
            while context.is_active():
                try:
                    event = subscriber.get(timeout=1)
                except queue.Empty:
                    continue
                if request.session_id and event.session_id != request.session_id:
                    continue
                yield event
        finally:
            self.broker.unsubscribe(subscriber)


class ASRClient:
    def __init__(self, addr: str, timeout_secs: int) -> None:
        self.channel = grpc.insecure_channel(addr)
        self.stub = agent_pb2_grpc.ASRStub(self.channel)
        self.timeout_secs = timeout_secs

    def close(self) -> None:
        self.channel.close()

    def transcribe(self, pcm_s16le: bytes, sample_rate_hz: int) -> tuple[str, float]:
        chunk_bytes = 3200

        def chunks():
            for offset in range(0, len(pcm_s16le), chunk_bytes):
                yield agent_pb2.AudioChunk(
                    pcm_s16le=pcm_s16le[offset : offset + chunk_bytes],
                    sample_rate_hz=sample_rate_hz,
                )

        final_text = ""
        confidence = 0.0
        for result in self.stub.StreamTranscribe(chunks(), timeout=self.timeout_secs):
            final_text = result.text
            confidence = result.confidence
        return final_text.strip(), confidence


class OrchestratorClient:
    def __init__(self, addr: str, timeout_secs: int) -> None:
        self.channel = grpc.insecure_channel(addr)
        self.stub = agent_pb2_grpc.OrchestratorStub(self.channel)
        self.timeout_secs = timeout_secs

    def close(self) -> None:
        self.channel.close()

    def chat(self, session_id: str, prompt: str) -> str:
        response = self.stub.Chat(
            agent_pb2.ChatRequest(
                session_id=session_id,
                messages=[agent_pb2.ChatMessage(role="user", content=prompt)],
            ),
            timeout=self.timeout_secs,
        )
        return response.text.strip()


class PulseRecorder:
    def __init__(self, sample_rate_hz: int, source: str) -> None:
        self.sample_rate_hz = sample_rate_hz
        self.source = source or "default"
        self.resolved_source: Optional[str] = None
        self.proc: Optional[subprocess.Popen] = None
        self._lock = threading.Lock()
        self._last_issue_log_at = 0.0

    def _log_issue(self, message: str, *args) -> None:
        now = time.time()
        if now-self._last_issue_log_at < 5:
            return
        self._last_issue_log_at = now
        logging.warning(message, *args)

    def resolve_source(self) -> Optional[str]:
        if self.resolved_source:
            return self.resolved_source
        if self.source and self.source != "default":
            self.resolved_source = self.source
            return self.resolved_source
        try:
            proc = subprocess.run(
                ["pactl", "info"],
                check=True,
                capture_output=True,
                text=True,
            )
        except Exception:
            logging.exception("wakeword.pulse_info_failed")
            return None
        for line in proc.stdout.splitlines():
            if not line.startswith("Default Source:"):
                continue
            resolved = line.split(":", 1)[1].strip()
            if resolved:
                self.resolved_source = resolved
                logging.debug("wakeword.audio_source_resolved requested=%s resolved=%s", self.source, resolved)
                return resolved
        logging.debug("wakeword.audio_source_unresolved requested=%s", self.source)
        return None

    def _log_stderr(self, pipe) -> None:
        for raw_line in iter(pipe.readline, b""):
            line = raw_line.decode("utf-8", "replace").strip()
            if line:
                self._log_issue("wakeword.parec %s", line)

    def ensure_started(self) -> None:
        with self._lock:
            if self.proc is not None and self.proc.poll() is None:
                return
            cmd = [
                "parec",
                "--raw",
                "--channels=1",
                "--format=s16le",
                "--rate",
                str(self.sample_rate_hz),
            ]
            source_name = self.resolve_source()
            if source_name:
                cmd.extend(["--device", source_name])
            logging.debug(
                "wakeword.audio_start source=%s resolved_source=%s sample_rate_hz=%d",
                self.source,
                source_name or "<pulse-default>",
                self.sample_rate_hz,
            )
            self.proc = subprocess.Popen(
                cmd,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                bufsize=0,
            )
            assert self.proc.stderr is not None
            threading.Thread(target=self._log_stderr, args=(self.proc.stderr,), daemon=True).start()

    def restart(self) -> None:
        with self._lock:
            if self.proc is None:
                return
            try:
                self.proc.terminate()
                self.proc.wait(timeout=1)
            except Exception:
                self.proc.kill()
            finally:
                self.proc = None

    def read_exact(self, size: int) -> bytes:
        while True:
            self.ensure_started()
            assert self.proc is not None and self.proc.stdout is not None
            chunks = bytearray()
            while len(chunks) < size:
                remaining = size - len(chunks)
                data = self.proc.stdout.read(remaining)
                if not data:
                    self._log_issue("wakeword.audio_restart bytes=%d expected=%d", len(chunks), size)
                    self.restart()
                    break
                chunks.extend(data)
            if len(chunks) == size:
                return bytes(chunks)

    def stop(self) -> None:
        self.restart()


class OpenWakeWordDetector:
    def __init__(self) -> None:
        self.mode = os.getenv("WAKEWORD_DETECTION_MODE", "hybrid").strip().lower() or "hybrid"
        self.sample_rate_hz = env_int("WAKEWORD_SAMPLE_RATE_HZ", 16000)
        self.frame_ms = env_int("WAKEWORD_OPENWAKEWORD_FRAME_MS", 80)
        self.frame_bytes = int(self.sample_rate_hz * (self.frame_ms / 1000.0) * 2)
        self.threshold = env_float("WAKEWORD_THRESHOLD", 0.5)
        self.vad_threshold = env_float("WAKEWORD_VAD_THRESHOLD", 0.0)
        self.debounce_secs = env_float("WAKEWORD_DEBOUNCE_SECS", 2.0)
        self.model_path = os.getenv("WAKEWORD_MODEL_PATH", "").strip()
        self.model: Optional[OpenWakeWordModel] = None
        self.model_label = ""
        self.last_trigger_at = 0.0
        self.active = False

        if self.mode not in {"openwakeword", "hybrid"}:
            return
        if not self.model_path:
            logging.info("wakeword.openwakeword disabled reason=missing_model_path mode=%s", self.mode)
            return
        if not os.path.exists(self.model_path):
            logging.info("wakeword.openwakeword disabled reason=model_missing path=%s mode=%s", self.model_path, self.mode)
            return

        kwargs = {
            "wakeword_models": [self.model_path],
            "inference_framework": "onnx",
        }
        if self.vad_threshold > 0:
            kwargs["vad_threshold"] = self.vad_threshold

        self.model = OpenWakeWordModel(**kwargs)
        self.model_label = os.path.basename(self.model_path)
        self.active = True
        logging.info(
            "wakeword.openwakeword enabled model=%s threshold=%.2f vad_threshold=%.2f",
            self.model_label,
            self.threshold,
            self.vad_threshold,
        )

    def reset(self) -> None:
        if self.model is None:
            return
        reset_fn = getattr(self.model, "reset", None)
        if callable(reset_fn):
            reset_fn()

    def predict(self, frame: bytes) -> Optional[tuple[str, float]]:
        if self.model is None:
            return None

        predictions = self.model.predict(np.frombuffer(frame, dtype=np.int16))
        best_name = ""
        best_score = 0.0
        for name, raw_value in predictions.items():
            if isinstance(raw_value, np.ndarray):
                score = float(raw_value[-1]) if raw_value.size else 0.0
            elif isinstance(raw_value, (list, tuple)):
                score = float(raw_value[-1]) if raw_value else 0.0
            else:
                score = float(raw_value)
            if score > best_score:
                best_name = name
                best_score = score

        now = time.time()
        if best_score < self.threshold:
            return None
        if now - self.last_trigger_at < self.debounce_secs:
            return None

        self.last_trigger_at = now
        self.reset()
        return best_name or self.model_label or "openwakeword", best_score


class WakeLoop:
    def __init__(self, broker: EventBroker) -> None:
        self.broker = broker
        self.stop_event = threading.Event()
        self.detection_mode = os.getenv("WAKEWORD_DETECTION_MODE", "hybrid").strip().lower() or "hybrid"
        self.sample_rate_hz = env_int("WAKEWORD_SAMPLE_RATE_HZ", 16000)
        self.frame_ms = env_int("WAKEWORD_FRAME_MS", 100)
        self.preroll_ms = env_int("WAKEWORD_PREROLL_MS", 300)
        self.min_speech_ms = env_int("WAKEWORD_MIN_SPEECH_MS", 400)
        self.silence_ms = env_int("WAKEWORD_SILENCE_MS", 1200)
        self.max_utterance_ms = env_int("WAKEWORD_MAX_UTTERANCE_MS", 10000)
        self.speech_rms_threshold = env_int("WAKEWORD_SPEECH_RMS_THRESHOLD", 700)
        self.followup_enabled = env_bool("WAKEWORD_FOLLOWUP_ENABLED", True)
        self.followup_timeout_secs = env_float("WAKEWORD_FOLLOWUP_TIMEOUT_SECS", 12.0)
        self.followup_max_turns = env_int("WAKEWORD_FOLLOWUP_MAX_TURNS", 4)
        self.session_id = os.getenv("WAKEWORD_SESSION_ID", "voice-loop")
        self.wake_phrases = split_phrases(os.getenv("WAKEWORD_PHRASES", "hey beemo,hey bmo,okay beemo,ok beemo"))
        self.recorder = PulseRecorder(
            sample_rate_hz=self.sample_rate_hz,
            source=os.getenv("PULSE_SOURCE", "default"),
        )
        self.detector = OpenWakeWordDetector()
        self.asr_client = ASRClient(
            addr=os.getenv("ASR_ADDR", "eve-asr:5019"),
            timeout_secs=env_int("WAKEWORD_ASR_TIMEOUT_SECS", 60),
        )
        self.orchestrator_client = OrchestratorClient(
            addr=os.getenv("ORCH_ADDR", "eve-orchestrator:5013"),
            timeout_secs=env_int("WAKEWORD_ORCH_TIMEOUT_SECS", 120),
        )

    def stop(self) -> None:
        self.stop_event.set()
        self.recorder.stop()
        self.asr_client.close()
        self.orchestrator_client.close()

    def publish_wake(self, source: str) -> None:
        event = agent_pb2.WakeDetected(
            session_id=self.session_id,
            timestamp_unix_ms=int(time.time() * 1000),
            source=source,
        )
        self.broker.publish(event)

    def capture_utterance(
        self,
        seed_frames: Optional[list[bytes]] = None,
        started: bool = False,
        start_deadline_monotonic: Optional[float] = None,
    ) -> bytes:
        frame_bytes = int(self.sample_rate_hz * (self.frame_ms / 1000.0) * 2)
        preroll_frames = max(1, self.preroll_ms // self.frame_ms)
        min_speech_frames = max(1, self.min_speech_ms // self.frame_ms)
        silence_frames = max(1, self.silence_ms // self.frame_ms)
        max_frames = max(min_speech_frames + 1, self.max_utterance_ms // self.frame_ms)

        history = deque(maxlen=preroll_frames)
        utterance_frames = list(seed_frames or [])
        speech_frames = max(1, len(utterance_frames)) if started and utterance_frames else 0
        trailing_silence_frames = 0

        while not self.stop_event.is_set():
            if not started and start_deadline_monotonic is not None and time.monotonic() >= start_deadline_monotonic:
                return b""
            frame = self.recorder.read_exact(frame_bytes)
            rms = pcm16_rms(frame)
            is_speech = rms >= self.speech_rms_threshold
            if not started:
                history.append(frame)
                if not is_speech:
                    if start_deadline_monotonic is not None and time.monotonic() >= start_deadline_monotonic:
                        return b""
                    continue
                utterance_frames.extend(history)
                started = True
                speech_frames = 1
                trailing_silence_frames = 0
                continue

            utterance_frames.append(frame)
            if is_speech:
                speech_frames += 1
                trailing_silence_frames = 0
            else:
                trailing_silence_frames += 1

            if len(utterance_frames) >= max_frames:
                break
            if speech_frames >= min_speech_frames and trailing_silence_frames >= silence_frames:
                break

        return b"".join(utterance_frames)

    def resolve_prompt(self, transcript: str, require_trigger: bool = True) -> Optional[str]:
        if not require_trigger:
            prompt = transcript.strip()
            return prompt or None
        prompt = extract_prompt(transcript, self.wake_phrases)
        if prompt is not None:
            return prompt
        if self.detector.active:
            return transcript.strip()
        return None

    def handle_utterance(
        self,
        pcm_s16le: bytes,
        publish_source: Optional[str] = None,
        require_trigger: bool = True,
    ) -> Optional[str]:
        transcript, confidence = self.asr_client.transcribe(pcm_s16le, self.sample_rate_hz)
        if not transcript:
            logging.debug("wakeword.transcript_empty")
            return None

        prompt = self.resolve_prompt(transcript, require_trigger=require_trigger)
        if prompt is None:
            logging.info("wakeword.heard transcript=%r confidence=%.2f matched=false", transcript, confidence)
            return None

        if publish_source:
            self.publish_wake(publish_source)

        if not prompt:
            logging.info("wakeword.heard transcript=%r confidence=%.2f matched=true prompt=<empty>", transcript, confidence)
            return None

        if require_trigger:
            logging.info("wakeword.heard transcript=%r confidence=%.2f matched=true prompt=%r", transcript, confidence, prompt)
        else:
            logging.info("wakeword.followup_heard transcript=%r confidence=%.2f prompt=%r", transcript, confidence, prompt)
        response = self.orchestrator_client.chat(self.session_id, prompt)
        logging.info("wakeword.orchestrator_response text=%r", response)
        return response

    def run_followup_window(self, initial_response: str) -> None:
        if not self.followup_enabled:
            return
        if self.followup_timeout_secs <= 0 or self.followup_max_turns <= 0:
            return
        if not should_listen_for_followup(initial_response):
            return

        response = initial_response
        turns_used = 0
        while not self.stop_event.is_set() and turns_used < self.followup_max_turns:
            utterance = self.capture_utterance(start_deadline_monotonic=time.monotonic() + self.followup_timeout_secs)
            if not utterance:
                return
            response = self.handle_utterance(utterance, require_trigger=False) or ""
            if not response:
                return
            turns_used += 1
            if not should_listen_for_followup(response):
                return

    def run_forever(self) -> None:
        logging.info(
            "wakeword.loop start mode=%s phrases=%s openwakeword_active=%s",
            self.detection_mode,
            ",".join(self.wake_phrases),
            self.detector.active,
        )
        while not self.stop_event.is_set():
            try:
                if self.detector.active:
                    self.run_openwakeword_forever()
                else:
                    self.run_phrase_fallback_forever()
            except Exception:
                logging.exception("wakeword.loop_error")
                time.sleep(1)

    def run_phrase_fallback_forever(self) -> None:
        while not self.stop_event.is_set():
            utterance = self.capture_utterance()
            if not utterance:
                continue
            response = self.handle_utterance(utterance, publish_source="asr_phrase")
            if response:
                self.run_followup_window(response)

    def run_openwakeword_forever(self) -> None:
        history = deque(maxlen=max(1, self.preroll_ms // max(self.detector.frame_ms, 1)))
        while not self.stop_event.is_set():
            frame = self.recorder.read_exact(self.detector.frame_bytes)
            history.append(frame)
            triggered = self.detector.predict(frame)
            if triggered is None:
                continue
            model_name, score = triggered
            logging.info("wakeword.triggered source=openwakeword model=%s score=%.2f", model_name, score)
            self.publish_wake(f"openwakeword:{model_name}")
            utterance = self.capture_utterance(seed_frames=list(history), started=True)
            if not utterance:
                continue
            response = self.handle_utterance(utterance)
            if response:
                self.run_followup_window(response)


def main() -> int:
    logging.basicConfig(
        level=os.getenv("LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)s %(message)s",
    )

    broker = EventBroker()
    servicer = WakeWordServicer(broker)
    loop = WakeLoop(broker)

    host = os.getenv("WAKEWORD_HOST", "0.0.0.0")
    port = env_int("WAKEWORD_PORT", 5020)

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    agent_pb2_grpc.add_WakeWordServicer_to_server(servicer, server)
    server.add_insecure_port(f"{host}:{port}")
    server.start()
    logging.info("wakeword.server listening=%s:%d", host, port)

    worker = threading.Thread(target=loop.run_forever, daemon=True)
    worker.start()

    def stop_handler(signum, _frame):
        logging.info("wakeword.server stopping signal=%s", signum)
        loop.stop()
        server.stop(grace=2)

    signal.signal(signal.SIGINT, stop_handler)
    signal.signal(signal.SIGTERM, stop_handler)

    try:
        server.wait_for_termination()
    finally:
        loop.stop()

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
