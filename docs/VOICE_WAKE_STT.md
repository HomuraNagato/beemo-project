# Voice Wake + STT Prototype

This repo now includes a host-side listener that:

1. captures microphone audio on the host
2. transcribes speech with `faster-whisper`
3. looks for a wake phrase such as `hey beemo`
4. forwards the request to `eve-orchestrator`

For the first pass, wake detection is transcript-based rather than a dedicated wake-word model. That keeps the setup smaller while still allowing flows like:

- `hey beemo what time is it`
- `hey beemo`
- `what time is it`

The second form arms the listener briefly so the next utterance is sent to the orchestrator.

## Setup

Install the system packages that Python audio capture usually needs on Ubuntu/WSL:

```bash
sudo apt update
sudo apt install -y python3-pip python3-venv libportaudio2 portaudio19-dev
```

Create a repo-local virtualenv and install the Python requirements:

```bash
python3 -m venv .venv-voice
source .venv-voice/bin/activate
pip install -r requirements-voice.txt
```

## Start Order

1. Start `eve-vllm`
2. Start the `eve-orchestrator` container
3. Run `go run ./src/orchestrator` inside the `eve-orchestrator` container
4. Start the voice listener on the host

The listener forwards requests by running `grpcurl` inside the `eve-orchestrator` container, so the container must exist and the server must already be listening on `localhost:5013` inside that container.

## Usage

List audio devices first if needed:

```bash
source .venv-voice/bin/activate
python3 scripts/eve-listen.py --list-devices
```

Run the listener:

```bash
source .venv-voice/bin/activate
python3 scripts/eve-listen.py
```

Useful flags:

```bash
python3 scripts/eve-listen.py --wake-phrase "hey beemo" --wake-phrase "okay beemo"
python3 scripts/eve-listen.py --input-device 0
python3 scripts/eve-listen.py --save-audio-dir /tmp/beemo-audio
python3 scripts/eve-listen.py --asr-model base.en
python3 scripts/eve-listen.py --text "hey beemo what time is it"
```

## Notes

- The default ASR model is `tiny.en` to keep CPU usage and download size reasonable.
- On first run, `faster-whisper` will download the chosen Whisper model.
- `eve-orchestrator` logs remain the source of truth for what the orchestrator received and how it responded.
