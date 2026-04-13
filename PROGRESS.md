# Progress (Go Migration)

## Current Go Footprint
- `proto/agent.proto` defines the gRPC contracts for `Orchestrator`, `WakeWord`, `ASR`, `LLM`, `TTS`, `Vision`, and `Tools`.
- Generated Go protobuf and gRPC code already exists in `proto/gen/proto`.
- `scripts/gen_proto.sh` regenerates those stubs with `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc`.
- `src/orchestrator/main.go` implements `Orchestrator.Chat` and persists JSONL chat history under `memory/`.
- `src/orchestrator/config/config.go` loads orchestrator, LLM, grammar, history, and service-address settings from env.
- `src/orchestrator/llm/llm.go` talks to an OpenAI-compatible HTTP API for both chat completions and grammar-constrained completions.
- `src/orchestrator/tools/` contains the in-process tool layer, including `get_time` and a non-trivial `calculator`.
- `src/tui/main.go` provides a working terminal chat client against the gRPC orchestrator.
- `docker-compose.yaml` wires the orchestrator to a vLLM-based reasoning service and installation workflow, with other services still partially scaffolded.

## What Is Implemented
- Tool-decision flow is live: the orchestrator asks the LLM for a structured tool call, executes it locally, then asks the LLM for the final user-facing response.
- Pending-input flow is implemented: if a tool needs more fields, the orchestrator stores pending state per session and resumes after the next user reply.
- Tool grounding is implemented for calculator requests so unsupported or hallucinated fields can be stripped before execution.
- Current in-process tools:
  - `get_time`
  - `calculator` with support for arithmetic expressions, unit conversion, BMI, BMR, TDEE, pace, speed, and percentage calculations
- Terminal interaction is usable today through the TUI and the `scripts/eve-orchestrator.sh` / `scripts/eve-tui.sh` helpers.
- Unit and integration-style tests exist for the orchestrator flow, LLM client requests, and calculator/tool behavior.

## Missing Pieces
- Separate Go service implementations for `WakeWord`, `ASR`, `LLM`, `TTS`, `Vision`, and `UI` are still not present.
- `Orchestrator.StreamState` is defined in the proto contract but is not implemented in the current server.
- LLM output is still synchronous request/response from the orchestrator's perspective; streaming token handling is not implemented.
- Audio, wake-word detection, TTS playback, and camera/vision integration are not yet migrated into working Go services.
- Compose references for some planned services are incomplete; for example, `compose/vision` and `compose/ui` are referenced by `docker-compose.yaml` but are not present in the repository.

## Outline Mapping
- The service-oriented target architecture in `OUTLINE.md` is still the direction of travel.
- Step 1 from the outline, the orchestrator, is beyond scaffolding and is the main functional piece today.
- Step 2 is partially addressed only through the orchestrator's HTTP client to an external OpenAI-compatible LLM endpoint.
- Steps 3–8 remain mostly planned, with tools still running in-process and the other services not yet implemented as standalone Go services.

## Current Developer Workflow
1. Start or point at an OpenAI-compatible LLM endpoint via `.env` values such as `LLM_HTTP_URL`, `LLM_COMPLETIONS_URL`, and `LLM_MODEL`.
2. Run the orchestrator with `go run ./src/orchestrator`.
3. Talk to it with `go run ./src/tui` or `scripts/eve-orchestrator.sh`.
4. Run `go test ./...` to validate the current Go codebase.

## Recommended Next Steps (Go-first)
1. Implement `StreamState` and establish a consistent orchestrator state model so UIs can subscribe to runtime updates.
2. Decide whether the current external OpenAI-compatible LLM adapter remains the long-term interface or whether a dedicated `LLM` gRPC service should be introduced next.
3. Expand the tool layer deliberately, keeping tools in-process until service separation provides a clear operational benefit.
4. Either add the missing `compose/vision` and `compose/ui` directories or remove those compose entries until those services actually exist.
5. Pick the next real service migration in priority order: wake-word/ASR for end-to-end activation, or TTS/vision depending on the immediate product goal.
