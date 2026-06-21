# Voice Services

This repo now includes two containerized voice services:

- `eve-asr`: gRPC ASR service backed by `faster-whisper`
- `eve-wakeword`: microphone listener that can trigger either from `openwakeword` or from a transcript-based wake phrase such as `hey beemo`, then forwards the request to `eve-orchestrator`

## Why this shape

This follows the same broad interaction pattern used in [be-more-agent](https://github.com/brenpoly/be-more-agent):

- listen for speech
- record one utterance until silence
- transcribe
- gate on a wake phrase
- route the text to the orchestrator

The main difference is that this repo now splits those responsibilities into Docker services that use the existing gRPC contracts in `proto/agent.proto`.

`eve-wakeword` supports a hybrid mode:

- if `WAKEWORD_MODEL_PATH` points at a valid `openwakeword` model, it uses that as the first-stage trigger
- otherwise it falls back to transcript-based wake phrase matching

## Runtime Config

Stable wakeword defaults live in `config/wakeword.yaml`:

- `detector.mode: hybrid`
- `detector.model_path: /models/wakeword/wakeword.onnx`
- `detector.phrases: [hey beemo, hey bmo, okay beemo, ok beemo]`
- `detector.asr_aliases` for ASR mishearings such as `don't be mad`
- `audio.source: default`
- `audio.preroll_ms: 700`

`docker-compose.yaml` passes that file as `WAKEWORD_CONFIG=/config/wakeword.yaml`.
Use `.env` only for secrets and host-specific overrides such as `PULSE_SOCKET_PATH`.

## Bring-up

Start the full voice stack with the normal launcher:

```bash
./scripts/beemo-start.sh vllm-cpu
```

For targeted service restarts during development:

```bash
./scripts/beemo-restart.sh vllm-cpu eve-asr
./scripts/beemo-restart.sh vllm-cpu eve-orchestrator
./scripts/beemo-restart.sh vllm-cpu eve-wakeword
```

Watch orchestrator logs in another shell:

```bash
./scripts/beemo-logs.sh eve-orchestrator
```

Then speak a single utterance such as:

```text
hey beemo what time is it
```

## Notes

- `eve-wakeword` expects microphone access through PulseAudio/PipeWire. On SteamOS the default bind mount is `/run/user/1000/pulse/native`.
- The first `eve-asr` startup may download the `small.en` Whisper model into `/models/faster-whisper`.
- If you want reliable `hey beemo` detection with `openwakeword`, you will usually want a custom `.onnx` model at `/models/wakeword/wakeword.onnx`. The upstream project ships built-in models for common phrases like `alexa`, `hey mycroft`, and `hey jarvis`, and documents custom model training separately: [openWakeWord README](https://github.com/dscripka/openWakeWord).
