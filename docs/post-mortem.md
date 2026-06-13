# Memory Post-Mortem

This document captures what the current memory work achieved, where it became difficult, and what should be preserved or avoided when the memory system is removed and redesigned.

## Current Design

The current memory implementation is not a general conversation memory module. It is primarily an append-only store of structured observations scoped to a resolved subject.

The main pieces are:

- `src/orchestrator/memoryctx/`: in-memory and Postgres-backed stores for observations, aliases, relationships, active speakers, conversation messages, and a direct memory graph.
- `src/orchestrator/subjectctx/`: subject, speaker, pronoun, possessive, and relationship resolution.
- `src/orchestrator/tools/infer.go`: deterministic extraction of facts and recall requests.
- `src/orchestrator/main.go`: chat-turn integration, memory writes, memory reads, pending clarifications, route matching, and direct responses.
- `facts.yaml`: typed fact catalog for recallable attributes.
- `routes.yaml`: route-level memory read/write policy.
- `db/migrations/`: Postgres schema for subjects, aliases, observations, identity relationships, graph nodes/edges, graph values, and conversation messages.

An observation stores an attribute for a subject, with raw and canonical JSON values, source turn, source type, route/domain metadata, timestamp, and optional embedding. Examples include `weight`, `height`, `age_years`, `gender`, `activity_level`, `birthday`, `favorite_color`, and generic text facts such as `codename` or `project_motto`.

Memory is used in several ways:

- Hydrating calculator calls for BMI, BMR, and TDEE.
- Recalling direct facts through `memory_lookup`.
- Remembering weather location defaults.
- Persisting aliases and relationships across sessions.
- Tracking an active speaker so later `my` references can resolve to the right person.
- Performing semantic recall over embedded observations.
- Mirroring observations and relationships into a direct memory graph.

## What Went Well

The append-only observation model was a solid foundation. It preserved history instead of overwriting facts in place, which made correction handling and conflict detection possible.

The raw/canonical split was useful. Raw values kept user-facing units closer to what the user said, while canonical values made calculations and comparisons more reliable.

Route-level memory policy was a good idea. Marking calculator health routes and weather routes as memory-aware made read/write behavior more explicit than burying everything inside tool code.

Deterministic extraction helped reduce model dependency. Obvious facts such as weight, height, birthday, favorite color, and generic `my X is Y` statements could be stored and recalled without a full LLM path.

The system accumulated strong regression tests. The tests document many real edge cases: command words becoming fake subjects, relationship labels leaking into aliases, speaker switches inheriting the wrong relationship tree, bad model arguments being overridden by snapshots, and generic memory recall avoiding LLM fallback.

Provenance was handled better than a simple key-value store. Observations carry source turn, source type, route, domain, and timestamp, which are all worth preserving in a future design.

## What Went Bad

Memory became tightly coupled to the orchestrator. Subject resolution, route selection, deterministic inference, pending clarification, tool execution, direct responses, and final prompting all know about memory behavior.

Subject identity grew too complex for the surrounding architecture. Active speakers, scoped relationship subjects, aliases, pronouns, possessives, and session-local context all interact in subtle ways.

The graph layer was added before the product shape was clear. `memory_nodes`, `memory_edges`, and `memory_node_values` mirror useful information, but the app does not yet have a clear user-facing graph workflow that justifies the extra schema and code paths.

Memory writes are too implicit. Normal chat turns can create observations if regex extraction matches. That makes behavior convenient, but it also makes it harder to predict when the assistant will remember something.

The system relies heavily on regex and narrow heuristics. Many fixes are correct locally, but the total behavior is hard to reason about because there are many special cases.

Generic fact labels are risky. Supporting arbitrary `my X is Y` facts is powerful, but without a stronger memory model it can create noisy attributes, accidental facts, and unclear recall semantics.

The LLM and deterministic paths overlap awkwardly. The system can infer a tool call, ask the model for a tool call, retry, coerce memory lookups into calculator calls, ground calculator values, and then override values from snapshots. Each piece is defensible, but the combined control flow is hard to audit.

Calculator memory distorted the overall design. Much of memory exists to make BMI/BMR/TDEE follow-ups work. That caused a narrow health-calculation use case to drive general identity and memory architecture.

## Things To Preserve

Keep explicit provenance. Future memory records should retain source text, created time, confidence or source type, and whether the memory came from explicit user instruction, extraction, or tool output.

Keep raw and normalized values separate. This is especially useful for measurements, dates, names, and user-facing recall.

Keep subject scoping, but simplify it. A future system still needs to know who a fact is about, but it should have clearer boundaries between identity resolution and memory storage.

Keep correction history. The latest value should be easy to retrieve, but older values should remain available for auditing and conflict handling.

Keep ambiguity handling. When multiple subjects or conflicting values are plausible, asking a clarification is better than guessing.

Keep the regression cases. The current tests are valuable evidence of real failures and should guide the next implementation, even if the current code is deleted.

## Things To Avoid Next Time

Do not let memory become a cross-cutting concern inside the main chat flow. The orchestrator should call a clear memory interface instead of implementing memory policy inline.

Do not make every user statement eligible for implicit storage. Prefer explicit memory writes at first, then add carefully bounded extraction later.

Do not let one tool domain define the memory model. Calculator hydration should be a consumer of memory, not the reason memory exists.

Do not add a graph schema until there is a clear graph use case. Start with the simplest model that supports the actual app behavior.

Do not allow arbitrary generic facts without validation, namespaces, or inspection. If generic memory exists, users should be able to see and correct what was stored.

Do not mix subject identity, relationship management, memory recall, and response formatting in one module. These should be separate responsibilities with explicit contracts.

## Suggested Rebuild Principles

Start with a small memory API:

- `Remember(subject, key, value, provenance)`
- `Recall(subject, query_or_key)`
- `Forget(memory_id_or_key)`
- `List(subject)`
- `ResolveSubject(turn_context)`

Make writes explicit before making them automatic. For example, support "remember that..." and "my X is Y" only after the system can show what it stored.

Separate memory types:

- Profile facts: stable user/person attributes.
- Preferences: likes, dislikes, defaults.
- Session context: temporary conversational state.
- Episodic notes: dated events or conversations.
- Tool state: values remembered to support tools, such as default weather location.

Build an inspection surface early. Memory should be debuggable from the CLI or UI before adding more inference.

Define deletion and correction semantics up front. A future memory module should support correction as a first-class operation rather than relying only on latest observation wins.

Keep the first version boring. A clean, inspectable key-value/profile memory with provenance will likely be more useful than a broad semantic graph that is hard to trust.

## Open Questions

- Should memory be opt-in only, or should some categories be stored automatically?
- Should all memories be subject-scoped, or should some be global app preferences?
- What should the user-facing memory inspection UI look like?
- How should corrections be represented: new observation, tombstone, replacement pointer, or all three?
- When should semantic recall be used instead of direct key lookup?
- Should relationships be part of memory, identity, or a separate contact/person model?
- What is the minimum memory system needed for the polished no-memory app to later reintroduce memory cleanly?
