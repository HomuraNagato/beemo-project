#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import sys
import time
from pathlib import Path

from eve_listen_lib import (
    AudioRecorder,
    DEFAULT_WAKE_PHRASES,
    RecorderConfig,
    decide_wake_action,
    invoke_orchestrator,
    load_whisper_model,
    print_input_devices,
    save_wav,
    transcribe_audio,
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Listen for a wake phrase, transcribe speech, and forward it to eve-orchestrator."
    )
    parser.add_argument("--session-id", default="voice")
    parser.add_argument("--wake-phrase", action="append", dest="wake_phrases")
    parser.add_argument("--arm-seconds", type=float, default=8.0)
    parser.add_argument("--text", help="Bypass the microphone and process this transcript directly.")
    parser.add_argument("--list-devices", action="store_true")
    parser.add_argument("--input-device")
    parser.add_argument("--sample-rate", type=int, default=16000)
    parser.add_argument("--block-ms", type=int, default=150)
    parser.add_argument("--start-threshold", type=float, default=0.015)
    parser.add_argument("--start-blocks", type=int, default=2)
    parser.add_argument("--end-silence-sec", type=float, default=0.9)
    parser.add_argument("--min-speech-sec", type=float, default=0.4)
    parser.add_argument("--max-speech-sec", type=float, default=12.0)
    parser.add_argument("--preroll-sec", type=float, default=0.3)
    parser.add_argument("--language", default="en")
    parser.add_argument("--asr-model", default=os.getenv("EVE_VOICE_ASR_MODEL", "tiny.en"))
    parser.add_argument(
        "--compute-type",
        default=os.getenv("EVE_VOICE_COMPUTE_TYPE", "int8"),
    )
    parser.add_argument("--cpu-threads", type=int, default=max(1, os.cpu_count() or 1))
    parser.add_argument("--container", default="eve-orchestrator")
    parser.add_argument("--grpcurl-bin", default="grpcurl")
    parser.add_argument("--proto-path", default="/workspace/proto/agent.proto")
    parser.add_argument("--orch-addr", default="localhost:5013")
    parser.add_argument("--print-only", action="store_true")
    parser.add_argument("--save-audio-dir")
    parser.add_argument("--debug", action="store_true")
    return parser.parse_args()


def run_dispatch(args: argparse.Namespace, transcript: str, armed_until: float) -> float:
    wake_phrases = args.wake_phrases or list(DEFAULT_WAKE_PHRASES)
    decision = decide_wake_action(transcript, wake_phrases, armed_until, time.time())

    print(f'transcript: "{transcript}"')
    if decision.action == "ignore":
        print("wake phrase not detected; ignoring utterance")
        return armed_until

    if decision.action == "arm":
        next_armed = time.time() + args.arm_seconds
        print(
            f'wake phrase "{decision.matched_phrase}" detected; listening for a command for {args.arm_seconds:.1f}s'
        )
        return next_armed

    prompt = decision.command_text.strip()
    if not prompt:
        next_armed = time.time() + args.arm_seconds
        print("no command text found after wake phrase; staying armed")
        return next_armed

    print(f'forwarding prompt: "{prompt}"')
    if args.print_only:
        return 0.0

    response = invoke_orchestrator(
        prompt,
        session_id=args.session_id,
        container_name=args.container or None,
        grpcurl_bin=args.grpcurl_bin,
        proto_path=args.proto_path,
        orch_addr=args.orch_addr,
    )
    if response:
        print(f'orchestrator reply: {response.get("text", "")}')
    return 0.0


def main() -> int:
    args = parse_args()
    if args.list_devices:
        print_input_devices()
        return 0

    if args.text:
        run_dispatch(args, args.text, 0.0)
        return 0

    recorder = AudioRecorder(
        RecorderConfig(
            sample_rate=args.sample_rate,
            block_ms=args.block_ms,
            start_threshold=args.start_threshold,
            start_blocks=args.start_blocks,
            end_silence_sec=args.end_silence_sec,
            min_speech_sec=args.min_speech_sec,
            max_speech_sec=args.max_speech_sec,
            preroll_sec=args.preroll_sec,
            input_device=args.input_device,
            debug=args.debug,
        )
    )
    model = load_whisper_model(
        args.asr_model,
        compute_type=args.compute_type,
        cpu_threads=args.cpu_threads,
    )

    armed_until = 0.0
    save_dir = Path(args.save_audio_dir) if args.save_audio_dir else None

    print(
        f"voice listener ready: wake_phrases={args.wake_phrases or list(DEFAULT_WAKE_PHRASES)} "
        f"asr_model={args.asr_model} sample_rate={args.sample_rate}"
    )
    while True:
        audio = recorder.capture_utterance()
        if save_dir is not None:
            save_wav(save_dir / f"{int(time.time() * 1000)}.wav", audio, args.sample_rate)
        transcript = transcribe_audio(model, audio, language=args.language)
        if not transcript:
            if args.debug:
                print("transcript empty; ignoring utterance")
            continue
        armed_until = run_dispatch(args, transcript, armed_until)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        print("\nvoice listener stopped")
        raise SystemExit(0)
    except RuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(1)
