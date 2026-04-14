#!/usr/bin/env python3
from __future__ import annotations

import collections
import json
import math
import queue
import re
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Sequence


DEFAULT_WAKE_PHRASES = (
    "hey beemo",
    "okay beemo",
    "ok beemo",
    "hey bmo",
    "okay bmo",
    "ok bmo",
)


def normalize_for_match(text: str) -> str:
    cleaned = re.sub(r"[^a-z0-9]+", " ", text.lower())
    return " ".join(cleaned.split())


@dataclass(frozen=True)
class WakeDecision:
    action: str
    command_text: str
    matched_phrase: str


def decide_wake_action(
    transcript: str,
    wake_phrases: Sequence[str],
    armed_until: float,
    now: float,
) -> WakeDecision:
    normalized = normalize_for_match(transcript)
    normalized_phrases = [normalize_for_match(phrase) for phrase in wake_phrases]

    for phrase in normalized_phrases:
        if not phrase:
            continue
        if normalized == phrase:
            return WakeDecision(action="arm", command_text="", matched_phrase=phrase)
        if normalized.startswith(phrase + " "):
            return WakeDecision(
                action="dispatch",
                command_text=normalized[len(phrase) :].strip(),
                matched_phrase=phrase,
            )
        marker = " " + phrase + " "
        idx = normalized.find(marker)
        if idx >= 0:
            command = normalized[idx + len(marker) :].strip()
            if command:
                return WakeDecision(
                    action="dispatch",
                    command_text=command,
                    matched_phrase=phrase,
                )

    if armed_until > now and normalized:
        return WakeDecision(action="dispatch", command_text=normalized, matched_phrase="")

    return WakeDecision(action="ignore", command_text="", matched_phrase="")


def build_chat_payload(session_id: str, prompt: str) -> str:
    return json.dumps(
        {
            "session_id": session_id,
            "messages": [
                {
                    "role": "user",
                    "content": prompt,
                }
            ],
        }
    )


def build_grpcurl_command(
    payload: str,
    *,
    container_name: str | None,
    grpcurl_bin: str,
    proto_path: str,
    orch_addr: str,
) -> list[str]:
    base = [
        grpcurl_bin,
        "-plaintext",
        "-proto",
        proto_path,
        "-d",
        "@",
        orch_addr,
        "eve.Orchestrator/Chat",
    ]
    if container_name:
        return ["docker", "exec", "-i", container_name, *base]
    return base


def invoke_orchestrator(
    prompt: str,
    *,
    session_id: str,
    container_name: str | None,
    grpcurl_bin: str,
    proto_path: str,
    orch_addr: str,
) -> dict:
    payload = build_chat_payload(session_id, prompt)
    cmd = build_grpcurl_command(
        payload,
        container_name=container_name,
        grpcurl_bin=grpcurl_bin,
        proto_path=proto_path,
        orch_addr=orch_addr,
    )
    result = subprocess.run(
        cmd,
        input=payload.encode("utf-8"),
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        stderr = result.stderr.decode("utf-8", errors="replace").strip()
        stdout = result.stdout.decode("utf-8", errors="replace").strip()
        detail = stderr or stdout or f"exit code {result.returncode}"
        raise RuntimeError(f"orchestrator call failed: {detail}")

    text = result.stdout.decode("utf-8", errors="replace").strip()
    if not text:
        return {}
    return json.loads(text)


def _lazy_import_audio_modules():
    try:
        import numpy as np  # type: ignore
        import sounddevice as sd  # type: ignore
    except ImportError as exc:  # pragma: no cover - exercised manually
        raise RuntimeError(
            "missing audio dependencies. Create a venv and install requirements-voice.txt first."
        ) from exc
    return np, sd


def _lazy_import_asr():
    try:
        from faster_whisper import WhisperModel  # type: ignore
    except ImportError as exc:  # pragma: no cover - exercised manually
        raise RuntimeError(
            "missing ASR dependencies. Create a venv and install requirements-voice.txt first."
        ) from exc
    return WhisperModel


def print_input_devices() -> None:
    _, sd = _lazy_import_audio_modules()
    print(sd.query_devices())


@dataclass
class RecorderConfig:
    sample_rate: int
    block_ms: int
    start_threshold: float
    start_blocks: int
    end_silence_sec: float
    min_speech_sec: float
    max_speech_sec: float
    preroll_sec: float
    input_device: str | int | None
    debug: bool


class AudioRecorder:
    def __init__(self, cfg: RecorderConfig):
        self.cfg = cfg
        self.np, self.sd = _lazy_import_audio_modules()
        self.block_size = max(1, int(cfg.sample_rate * cfg.block_ms / 1000))
        self.end_silence_blocks = max(
            1, int(math.ceil(cfg.end_silence_sec * cfg.sample_rate / self.block_size))
        )
        self.preroll_blocks = max(
            1, int(math.ceil(cfg.preroll_sec * cfg.sample_rate / self.block_size))
        )
        self.q: queue.Queue = queue.Queue()

    def _callback(self, indata, frames, time_info, status) -> None:
        if status and self.cfg.debug:
            print(f"audio status: {status}", file=sys.stderr)
        self.q.put(indata.copy())

    def capture_utterance(self):
        recording = False
        loud_run = 0
        silent_run = 0
        captured = []
        preroll = collections.deque(maxlen=self.preroll_blocks)

        with self.sd.InputStream(
            samplerate=self.cfg.sample_rate,
            blocksize=self.block_size,
            channels=1,
            dtype="float32",
            device=self.cfg.input_device,
            callback=self._callback,
        ):
            while True:
                block = self.q.get()
                mono = block.reshape(-1)
                level = float(self.np.sqrt(self.np.mean(self.np.square(mono))))
                is_loud = level >= self.cfg.start_threshold

                if self.cfg.debug:
                    print(
                        f"audio block rms={level:.4f} recording={recording}",
                        file=sys.stderr,
                    )

                if not recording:
                    preroll.append(mono)
                    if is_loud:
                        loud_run += 1
                        if loud_run >= self.cfg.start_blocks:
                            recording = True
                            captured = list(preroll)
                            silent_run = 0
                    else:
                        loud_run = 0
                    continue

                captured.append(mono)
                if is_loud:
                    silent_run = 0
                else:
                    silent_run += 1

                duration = sum(len(chunk) for chunk in captured) / self.cfg.sample_rate
                if duration >= self.cfg.max_speech_sec or silent_run >= self.end_silence_blocks:
                    if duration < self.cfg.min_speech_sec:
                        recording = False
                        loud_run = 0
                        silent_run = 0
                        captured = []
                        preroll.clear()
                        continue
                    return self.np.concatenate(captured, axis=0)


def save_wav(path: Path, audio, sample_rate: int) -> None:
    import wave

    path.parent.mkdir(parents=True, exist_ok=True)
    clipped = audio.clip(-1.0, 1.0)
    pcm = (clipped * 32767.0).astype("<i2")
    with wave.open(str(path), "wb") as wav_file:
        wav_file.setnchannels(1)
        wav_file.setsampwidth(2)
        wav_file.setframerate(sample_rate)
        wav_file.writeframes(pcm.tobytes())


def transcribe_audio(model, audio, *, language: str, beam_size: int = 1) -> str:
    segments, _ = model.transcribe(
        audio,
        language=language,
        beam_size=beam_size,
        best_of=beam_size,
        temperature=0.0,
        vad_filter=False,
        condition_on_previous_text=False,
        without_timestamps=True,
    )
    parts = []
    for segment in segments:
        text = segment.text.strip()
        if text:
            parts.append(text)
    return " ".join(parts).strip()


def load_whisper_model(model_name: str, *, compute_type: str, cpu_threads: int):
    WhisperModel = _lazy_import_asr()
    return WhisperModel(
        model_name,
        device="cpu",
        compute_type=compute_type,
        cpu_threads=cpu_threads,
    )

