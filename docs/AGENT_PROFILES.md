# Agent profiles

Beemo has three execution profiles:

- `normal` is the persistent default and may use up to four model rounds.
- `research` is a one-request profile with up to eight rounds. Use `/research <question>` or an explicit request beginning with `research` or `investigate`.
- `code` is persistent, repository-scoped, and retains its 64-step limit.

`/internet` independently enables or disables web search for the current session. It does not change profiles. Use `/normal` to leave Code mode.

## Core context

Each Memory Palace tenant may have one compact, manually curated core-context record. Beemo includes it in normal, research, and code prompts but does not infer or silently save new memories.

Read or update it through Memory Palace:

```bash
curl -sS -H 'X-Memory-User-Key: nagato' http://127.0.0.1:8013/v1/core-context

curl -sS -X PUT \
  -H 'Content-Type: application/json' \
  -H 'X-Memory-User-Key: nagato' \
  --data '{"body":"Preferred name: Nagato.\nTimezone: America/New_York."}' \
  http://127.0.0.1:8013/v1/core-context
```

The orchestrator caches the record for one minute. Missing or unavailable core context does not block a response.
