# Progress (Go Migration)

## Current Go Footprint
- `proto/agent.proto` defines the gRPC contracts for `Orchestrator`, `WakeWord`, `ASR`, `LLM`, `TTS`, `Vision`, and `Tools`.
- Generated Go protobuf and gRPC code exists in `proto/gen/proto`; `scripts/gen_proto.sh` regenerates it.
- `src/orchestrator/main.go` implements `Orchestrator.Chat`, JSONL chat history, retrieval-assisted tool routing, local tool execution, pending-input resume, and final response generation.
- `src/orchestrator/config/config.go` loads orchestrator, LLM, embedding, routing, grammar, history, and service-address settings from `config/config.yaml`.
- `src/orchestrator/db/` contains simple Postgres connection and migration helpers for optional route catalog persistence.
- `src/orchestrator/llm/llm.go` talks to OpenAI-compatible HTTP APIs for chat completions and grammar-constrained completions.
- `src/orchestrator/embedding/` contains the OpenAI-compatible embeddings HTTP client used by the route selector.
- `src/orchestrator/routing/` loads `routes.yaml`, warms a route index at startup, and does hierarchical domain-first retrieval.
- `src/orchestrator/tools/` contains the in-process tool layer, including `get_time`, `weather`, `older_sister`, and `calculator`.
- `src/tui/main.go` provides a working terminal chat client against the gRPC orchestrator.
- `docker-compose.yaml` wires the orchestrator to vLLM-based reasoning, a dedicated `eve-embedding` vLLM service, installation workflow, Python-backed `eve-asr` / `eve-wakeword` scaffolds, and host access to an optional local Postgres.

## What Is Implemented
- Tool-decision flow is live: the orchestrator asks the LLM for a structured tool call, executes it locally, then asks the LLM for the final user-facing response.
- Pending-input flow is implemented: if a tool needs more fields, the orchestrator stores pending state per session and resumes after the next user reply.
- Tool grounding is implemented for calculator requests so unsupported or hallucinated fields can be stripped before execution.
- Retrieval-assisted routing is implemented: the orchestrator embeds the user request, retrieves top route candidates from `routes.yaml`, and narrows the tool-decision prompt before calling the reasoning model.
- Hierarchical routing is implemented: retrieval selects top domains first and then ranks routes inside those domains.
- Route warmup is implemented at startup: the orchestrator probes the embedding service and precomputes the route index.
- Optional route catalog sync is wired through Postgres: route documents and embeddings can be inserted once per route/model and skipped on later startups.
- A first end-to-end voice path exists in Docker: `eve-wakeword` captures microphone audio from PulseAudio, records one utterance until silence, sends PCM to `eve-asr`, strips a configurable wake phrase, and forwards the remaining text to `Orchestrator.Chat`.
- `eve-orchestrator` is self-running in Docker through `compose/orchestrator/Dockerfile`.
- `scripts/beemo-start.sh` provides a button-friendly startup path with GPU/CPU overlays, health waits, and recent-log failures.
- The Makefile exposes `start`, `start-gpu`, `start-cpu`, `stop`, and `logs` targets.
- `scripts/eve-orchestrator.sh` can fall back to `docker exec eve-orchestrator grpcurl` when `grpcurl` is not installed on the host.
- Current in-process tools:
  - `get_time`
  - `calculator` with arithmetic expressions, unit conversion, BMI, BMR, TDEE, pace, speed, and percentage calculations
  - `weather` through Open-Meteo forecast/geocoding
  - `older_sister`, an OpenAI Responses API backed tool that can use web search for current external information
- Terminal interaction is usable through the TUI and the `scripts/eve-orchestrator.sh` / `scripts/eve-tui.sh` helpers. `scripts/beemo-start.sh --no-voice` starts the core services and then opens the TUI.
- Unit and integration-style tests exist for the orchestrator flow, LLM client requests, routing, and calculator/tool behavior.

## Missing Pieces
- Separate Go service implementations for `WakeWord`, `ASR`, `LLM`, `TTS`, `Vision`, and `UI` are still not present. `WakeWord` and `ASR` have Python service implementations, but they are not yet migrated to Go.
- `Orchestrator.StreamState` is defined in the proto contract but is not implemented in the current server.
- LLM output is still synchronous request/response from the orchestrator's perspective; streaming token handling is not implemented.
- Route retrieval still uses the warmed in-process route index at runtime rather than querying `route_embeddings`.
- Audio, wake-word detection, TTS playback, and camera/vision integration are not yet migrated into working Go services. The current voice path is a Python/Docker prototype rather than a Go migration.
- Compose references for some planned services are incomplete; for example, `compose/vision` and `compose/ui` are referenced by `docker-compose.yaml` but are not present in the repository.

## Current Developer Workflow
1. Start the core stack with `make start` or `./scripts/beemo-start.sh gpu`.
2. Use `make start-cpu` or `./scripts/beemo-start.sh cpu` for the CPU compose overlay.
3. Edit `config/config.yaml` for app/model URLs, route settings, weather defaults, and tool model settings.
4. For the `older_sister` tool, set `OPENAI_API_KEY` in `.env` or the shell.
5. Run the orchestrator manually with `go run ./src/orchestrator` only when bypassing Docker.
6. Talk to it with `go run ./src/tui` or `scripts/eve-orchestrator.sh`.
7. Run `go test ./...` to validate the current Go codebase.

## Recommended Next Steps
1. Polish the no-profile baseline: tighten prompts, route wording, calculator validation, and user-facing error states.
2. Implement `StreamState` and establish a consistent orchestrator state model so UIs can subscribe to runtime updates.
3. Decide whether the current external OpenAI-compatible adapters remain the long-term interface or whether dedicated `LLM` / `Embedding` gRPC services should be introduced next.
4. Either add the missing `compose/vision` and `compose/ui` directories or remove those compose entries until those services actually exist.
5. Harden the voice path on real hardware: confirm PulseAudio device discovery, tune speech thresholds, and decide whether to keep ASR-driven wake-phrase detection or swap in a dedicated wake-word model.
