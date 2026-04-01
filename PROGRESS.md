# Progress (Go Migration)

## Current Go Footprint
- `proto/agent.proto` defines all services and messages.
- `scripts/gen_proto.sh` exists to generate Go stubs.
- `src/orchestrator/main.go` is a stub that dials services from env vars and idles.
- `src/orchestrator/config/config.go` loads service addresses from env.
- `go.mod` includes `grpc` and `protobuf`.

## Missing Pieces
- Generated Go gRPC code in `proto/gen` (directory exists but is empty).
- Any Go service implementations (WakeWord, ASR, LLM, TTS, Tools, Vision, UI).
- Orchestrator logic beyond dialing: state machine, memory, tool routing, LLM streaming, TTS playback, etc.
- CLI/config for orchestrator beyond env vars.

## Outline Mapping
- Target architecture contracts are defined, but only scaffolding exists.
- Implementation Plan step 1 (Orchestrator) has started but only as connectivity checks.
- Steps 2–8 (LLM service, Tools, Vision, UI, WakeWord, ASR, TTS) are not started in Go.

## Recommended Next Steps (Go-first)
1. Generate Go stubs into `proto/gen` via `scripts/gen_proto.sh` and wire them into `src/orchestrator` imports.
2. Implement minimal Go orchestrator loop:
   - Connect to WakeWord stream.
   - Call Tools (start with `get_time`) for a basic request/response path.
3. Build a minimal Go Tools service first (fastest to validate gRPC + routing).
4. Add LLM service adapter next (even a mock stream) to validate streaming.
5. Add TTS service last, once the LLM stream is stable.
