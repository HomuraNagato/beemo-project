# Progress (Go Migration)

## Current Go Footprint
- `proto/agent.proto` defines the gRPC contracts for `Orchestrator`, `WakeWord`, `ASR`, `LLM`, `TTS`, `Vision`, and `Tools`.
- Generated Go protobuf and gRPC code already exists in `proto/gen/proto`.
- `scripts/gen_proto.sh` regenerates those stubs with `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc`.
- `src/orchestrator/main.go` implements `Orchestrator.Chat` and persists JSONL chat history under `memory/`.
- `src/orchestrator/config/config.go` loads orchestrator, LLM, embedding, routing, grammar, history, and service-address settings from env.
- `src/orchestrator/db/` contains simple Postgres connection and migration helpers for the optional Postgres-backed `beemo` database.
- `src/orchestrator/llm/llm.go` talks to an OpenAI-compatible HTTP API for both chat completions and grammar-constrained completions.
- `src/orchestrator/embedding/` contains the OpenAI-compatible embeddings HTTP client used by the route selector.
- `src/orchestrator/routing/` loads `routes.yaml`, warms a route index at startup, and does hierarchical domain-first retrieval.
- `src/orchestrator/subjectctx/` resolves `self`, named people, relation aliases, and possessive health references into session-scoped subject IDs.
- `src/orchestrator/memoryctx/` maintains the subject-scoped observation store used for calculator follow-ups, with an in-memory path and a Postgres-backed path.
- `src/orchestrator/tools/` contains the in-process tool layer, including `get_time` and a non-trivial `calculator`.
- `src/tui/main.go` provides a working terminal chat client against the gRPC orchestrator.
- `routes.yaml` defines the current route catalog, including domains and calculator/time routes for retrieval-assisted routing.
- `docker-compose.yaml` wires the orchestrator to a vLLM-based reasoning service, a dedicated `eve-embedding` vLLM service, installation workflow, Python-backed `eve-asr` / `eve-wakeword` scaffolds for voice activation, and host access to an already-running local Postgres via `host.docker.internal`.
- `docker-compose.pensieve.yaml` is an optional overlay that starts a local `pensieve` Postgres service with `pgvector`, using `beemo` as the application database on fresh machines.

## What Is Implemented
- Tool-decision flow is live: the orchestrator asks the LLM for a structured tool call, executes it locally, then asks the LLM for the final user-facing response.
- Pending-input flow is implemented: if a tool needs more fields, the orchestrator stores pending state per session and resumes after the next user reply.
- Tool grounding is implemented for calculator requests so unsupported or hallucinated fields can be stripped before execution.
- Retrieval-assisted routing is implemented: the orchestrator embeds the user request, retrieves top route candidates from `routes.yaml`, and narrows the tool-decision prompt before calling the reasoning model.
- Hierarchical routing is implemented: retrieval selects top domains first and then ranks routes inside those domains.
- Route warmup is implemented at startup: the orchestrator expects the embedding service to be online, probes it, and precomputes the route index in memory.
- A local OpenAI-compatible embedding path is implemented through the separate `eve-embedding` vLLM service.
- Session-scoped subject memory is implemented for calculator health routes:
  - subject resolution for `self`, named people, relation aliases, pronouns, and possessive forms such as `Serene's`
  - append-only observations keyed by `session_id + subject_id`
  - route-aware memory policy in `routes.yaml` for calculator `bmi` / `bmr` / `tdee`
  - richer observation records with `domain`, `route`, `source_turn`, `source_type`, `raw_value`, `canonical_value`, and `created_at`
  - hydration of BMI/BMR/TDEE inputs from subject memory
  - deterministic-first health-slot resolution with canonicalization before execution
  - conflict detection for competing explicit values, with clarification before calculator execution when memory is ambiguous
- Optional Postgres persistence is wired for subject observations:
  - `DATABASE_URL` enables the Postgres-backed store
  - orchestrator bootstraps the target database if the database server is reachable but the named DB does not exist yet
  - orchestrator runs SQL migrations from `db/migrations` at startup
  - the same codebase can either connect to an existing local Postgres server and create/use the `beemo` database, or start one via `docker-compose.pensieve.yaml`
- Initial Postgres schema is present for:
  - `subjects`
  - `subject_aliases`
  - `observations`
  - `route_documents`
  - `route_embeddings`
- Route catalog sync is now wired at selector warmup:
  - missing `route_documents` rows are inserted once per `route_id`
  - missing `route_embeddings` rows for the active embedding model are inserted once per `route_id + model`
  - existing rows are skipped rather than overwritten
- A first end-to-end voice path now exists in Docker: `eve-wakeword` captures microphone audio from PulseAudio, records one utterance until silence, sends PCM to `eve-asr`, strips a configurable wake phrase, and forwards the remaining text to `Orchestrator.Chat`.
- Current in-process tools:
  - `get_time`
  - `calculator` with support for arithmetic expressions, unit conversion, BMI, BMR, TDEE, pace, speed, and percentage calculations
- Terminal interaction is usable today through the TUI and the `scripts/eve-orchestrator.sh` / `scripts/eve-tui.sh` helpers.
- Unit and integration-style tests exist for the orchestrator flow, LLM client requests, and calculator/tool behavior.

## Missing Pieces
- Separate Go service implementations for `WakeWord`, `ASR`, `LLM`, `TTS`, `Vision`, and `UI` are still not present. `WakeWord` and `ASR` now have Python service implementations, but they are not yet migrated to Go.
- `Orchestrator.StreamState` is defined in the proto contract but is not implemented in the current server.
- LLM output is still synchronous request/response from the orchestrator's perspective; streaming token handling is not implemented.
- The current memory layer is still narrow even though Postgres persistence is now available:
  - no memory inspection/debug endpoint yet
  - no general non-calculator memory hydration yet
  - route retrieval still uses the in-memory warmup index rather than querying `route_embeddings`
  - no episodic memory layer yet
  - no alias persistence/readback path yet
- Audio, wake-word detection, TTS playback, and camera/vision integration are not yet migrated into working Go services. The current voice path is a Python/Docker prototype rather than a Go migration.
- Compose references for some planned services are incomplete; for example, `compose/vision` and `compose/ui` are referenced by `docker-compose.yaml` but are not present in the repository.

## Outline Mapping
- The service-oriented target architecture in `OUTLINE.md` is still the direction of travel.
- Step 1 from the outline, the orchestrator, is beyond scaffolding and is the main functional piece today.
- Step 2 is partially addressed through the orchestrator's HTTP clients to external OpenAI-compatible reasoning and embedding endpoints.
- Steps 3–8 remain mostly planned, with tools still running in-process and the other services not yet implemented as standalone Go services.

## Current Developer Workflow
1. Start or point at an OpenAI-compatible reasoning endpoint and the local embedding endpoint via `.env`.
2. Choose a database target:
   - existing local Postgres on the Docker host: `DATABASE_URL=postgres://postgres:postgres@host.docker.internal:5438/beemo?sslmode=disable`
   - bundled local Postgres on a fresh machine: `docker compose -f docker-compose.yaml -f docker-compose.pensieve.yaml up -d pensieve`, then `DATABASE_URL=postgres://postgres:postgres@pensieve:5432/beemo?sslmode=disable`
   - host-shell `go run` instead of Docker: use `127.0.0.1:5438/beemo` rather than `host.docker.internal`
3. Run the orchestrator with `go run ./src/orchestrator` or via the existing Docker workflow.
4. Talk to it with `go run ./src/tui` or `scripts/eve-orchestrator.sh`.
5. Run `go test ./...` to validate the current Go codebase.

## Recommended Next Steps (Go-first)
1. Add a small inspection/debug surface for `session -> subject -> snapshot -> conflicts` so the Postgres-backed memory path is easy to inspect live.
2. Persist and read back aliases in Postgres so subject resolution survives orchestrator restarts when `DATABASE_URL` is set.
3. Generalize route-aware memory reads/writes beyond calculator health routes.
4. Extend the route catalog sync so Postgres stores the full retrieval corpus, not just one base route descriptor per route.
5. Implement `StreamState` and establish a consistent orchestrator state model so UIs can subscribe to runtime updates.
6. Decide whether the current external OpenAI-compatible adapters remain the long-term interface or whether dedicated `LLM` / `Embedding` gRPC services should be introduced next.
7. Either add the missing `compose/vision` and `compose/ui` directories or remove those compose entries until those services actually exist.
8. Harden the new voice path on real hardware: confirm PulseAudio device discovery, tune speech thresholds, and decide whether to keep ASR-driven wake-phrase detection or swap in a dedicated wake-word model.
