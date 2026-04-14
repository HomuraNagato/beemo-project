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
- `PULSE_SOCKET_PATH=/mnt/wslg/PulseServer`
- `PULSE_SOURCE=default`

## Bring-up

1. Start the reasoning service you already use:

```bash
dockrefresh eve-vllm cpu
```

2. Start the ASR service:

```bash
dockrefresh eve-asr
```

3. Start the orchestrator container and launch the server into the container log stream:

```bash
dockrefresh eve-orchestrator
./scripts/eve-orchestrator-run.sh
```

4. Start the wakeword listener:

```bash
dockrefresh eve-wakeword
```

5. Watch orchestrator logs in another shell:

```bash
dockwatch eve-orchestrator
```

Then speak a single utterance such as:

```text
hey beemo what time is it
```

## Notes

- `eve-wakeword` expects microphone access through PulseAudio. On WSLg the default bind mount is `/mnt/wslg/PulseServer`.
- The first `eve-asr` startup may download the `small.en` Whisper model into `/models/faster-whisper`.
- If you want reliable `hey beemo` detection with `openwakeword`, you will usually want a custom `.onnx` model at `/models/wakeword/wakeword.onnx`. The upstream project ships built-in models for common phrases like `alexa`, `hey mycroft`, and `hey jarvis`, and documents custom model training separately: [openWakeWord README](https://github.com/dscripka/openWakeWord).
- `./scripts/eve-orchestrator-run.sh` is a stopgap until `eve-orchestrator` gets its own entrypoint. It starts `go run ./src/orchestrator` inside the container and redirects output into `docker logs`.
