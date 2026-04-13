# Architecture Notes and Refactor Outline

## Current Architecture (As Implemented)
- Single-process Tkinter app (`agent.py`) owns UI, state machine, wake word, audio IO, ASR, LLM, tools, vision, TTS, and memory.
- All orchestration logic lives inside `BotGUI`.
- External engines are invoked via `subprocess` (Whisper CLI, Piper CLI, rpicam).

## Core Components and Responsibilities
- UI / State Machine: `BotGUI` handles face animation, HUD, state transitions, and status text.
- Wake Word & PTT: `detect_wake_word_or_ptt()` uses `openwakeword` + `sounddevice`.
- Audio Recording: `record_voice_adaptive()` and `record_voice_ptt()`.
- ASR: `transcribe_audio()` calls `whisper.cpp` CLI.
- LLM Chat: `chat_and_respond()` streams responses from `ollama`.
- Action Router: `execute_action_and_get_result()` handles `get_time`, `search_web`, `capture_image`.
- Vision Capture: `capture_image()` uses `rpicam-still` and rotates image.
- TTS: `speak()` uses Piper CLI and streams audio to the output device.
- Memory: `load_chat_history()` / `save_chat_history()` read/write `memory.json`.

## High-Level Data Flow
1. Wake word or PTT triggers listening.
2. Record audio -> save WAV.
3. Whisper transcribes -> text.
4. LLM chat streams response.
5. If LLM emits action JSON, tool router executes:
   - Search -> DDGS
   - Capture -> rpicam
   - Time -> local datetime
6. LLM summarizes tool result.
7. TTS speaks; UI updates; memory written.

## Tight Coupling Points
- Everything is owned by `BotGUI`, with threading and shared state flags.
- UI thread and audio/LLM operations are tightly intertwined.
- External tool invocations happen directly in the UI process.
- Config mismatch: `config.json` uses `system_prompt`, but `agent.py` expects `system_prompt_extras`.

---

## Target Architecture (Orchestrator + Services)
Goal: Split into services that wait/listen while a main orchestrator routes messages.

### Services
1. Orchestrator (target: Python/script)
   - Owns the state machine and conversation memory.
   - Routes requests to services and aggregates responses.
   - Emits UI updates.
2. Wake Word Service (target: Python/script)
   - Continuous mic monitoring.
   - Emits `wake_detected` events.
3. ASR Service (Whisper) (target: `whisper.cpp` CLI/server)
   - Accepts audio buffers or file paths.
   - Returns transcripts and confidence.
4. LLM Service (Ollama) (target: `llama.cpp` server)
   - Accepts messages; returns streaming chunks.
5. TTS Service (Piper) (target: Python/script)
   - Accepts text; returns audio stream or handles playback.
6. Vision Service (optional) (target: Python/script)
   - Captures images and returns file paths.
7. Tool execution (initially in-process inside orchestrator)
   - Executes `search_web`, `get_time`, etc.
   - Can be split into a service later if isolation or deployment needs justify it.
8. UI Service (target: Python/script)
   - Handles rendering/animation, receives state + text updates.

---

## IPC / Message Bus Options
- gRPC over Unix domain sockets (chosen): Typed APIs, local-only friendly, no broker, supports streaming for LLM/TTS.
- ZeroMQ: Lightweight, fast, flexible patterns (`PUB/SUB`, `REQ/REP`), but untyped and DIY reliability.
- Redis Pub/Sub + Streams: Easy visibility and replay, but requires a broker service (not desired here).
- MQTT: Simple broker, good for device expansion.

---

## Suggested gRPC Contracts (Minimal)
Services:
1. Orchestrator (optional gRPC facade for UI/tests)
   - `StreamState(StateRequest) -> stream StateUpdate`
2. WakeWord
   - `StreamWake(WakeRequest) -> stream WakeDetected`
3. ASR
   - `StreamTranscribe(stream AudioChunk) -> stream TranscribeResult`
4. LLM
   - `Chat(ChatRequest) -> stream ChatChunk`
5. TTS
   - `Speak(SpeakRequest) -> stream AudioChunk`
6. Vision
   - `Capture(CaptureRequest) -> CaptureResult`
Note: tool execution is currently internal to the orchestrator, so a gRPC `Tools` contract is optional rather than required.

Core Messages:
- `WakeDetected { timestamp, source }`
- `StateUpdate { state, message }`
- `TranscribeResult { text, confidence }`
- `ChatRequest { messages[], images[]?, options? }`
- `ChatChunk { text }`
- `SpeakRequest { text }`
- `AudioChunk { pcm_s16le_bytes, sample_rate_hz }`
- `CaptureResult { image_path }`
- `ToolRequest { action, value }`
- `ToolResult { action, result }`

---

## Orchestrator Flow (Service-Oriented, gRPC)
1. Subscribe to `WakeWord.StreamWake` and wait for `WakeDetected`.
2. Stream mic audio to `ASR.StreamTranscribe(...)`.
3. Receive `TranscribeResult` from ASR.
4. Append to memory; call `LLM.Chat(...)` and stream `ChatChunk`.
5. If tool JSON detected, execute the matching local tool handler -> `ToolResult`.
6. Optionally summarize tool result via `LLM.Chat(...)`.
7. Send text to `TTS.Speak(...)` (stream `AudioChunk`) + UI updates.

---

## Implementation Plan (Service Order)
1. Orchestrator (gRPC client, state machine, memory, tool routing).
2. LLM service (`llama.cpp` server) + client adapter in orchestrator.
3. Expand in-process tool execution (search/time/etc).
4. Vision service + client adapter in orchestrator.
5. UI service (optional) + state update wiring.
6. Wake Word service (last; requires mic access).
7. ASR service with `StreamTranscribe` (last; requires mic access).
8. TTS service (last; requires speaker/device testing).

---

## Risks / Constraints
- Audio device contention: keep mic capture and speaker output single-owner processes.
- ASR service owns the mic device when using streaming transcription.
- Threading to process boundary: convert shared flags/events into messages.
- Action parsing is currently regex-based; consider stricter JSON-only tool responses in orchestrator.

---

## Future Planner Layer
- Add a planner/organizer stage ahead of tool execution for requests that need multiple steps or multiple tools.
- Planner output should break a request into a small ordered plan rather than forcing a single tool call.
- Executor stage should run one or more tool calls from that plan, collect structured results, and pass the full result set to the final response stage.
- Keep the simple direct single-tool path for trivial requests so latency stays low.
- Good candidates for planner-required queries:
  - multi-step research
  - chained calculations
  - calendar or scheduling workflows
  - requests that need clarification before selecting tools
