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

## Env knobs

Relevant defaults live in `.env` and `.env.example`:

- `ASR_MODEL=small.en`
- `WAKEWORD_DETECTION_MODE=hybrid`
- `WAKEWORD_MODEL_PATH=/models/wakeword/wakeword.onnx`
- `WAKEWORD_PHRASES=hey beemo,hey bmo,okay beemo,ok beemo`
- `WAKEWORD_ASR_ALIASES=don't be mad,dont be mad,hey bemo,hey bimo,okay bemo,ok bemo`
- `PULSE_SOCKET_PATH=/run/user/1000/pulse/native`
- `PULSE_SOURCE=default`

## Bring-up

Start the full voice stack with the normal launcher:

```bash
./scripts/beemo-start.sh vllm-gpu
```

For targeted service restarts during development:

```bash
./scripts/beemo-restart.sh vllm-gpu eve-asr
./scripts/beemo-restart.sh vllm-gpu eve-orchestrator
./scripts/beemo-restart.sh vllm-gpu eve-wakeword
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
