# Beemo Lifecycle

`beemo` is the single lifecycle entry point for the local Beemo application.
It keeps model and application services isolated in containers while presenting
one command surface.

Build or install it:

```sh
make build
make install
```

Start the Garnetmoon stack, including Memory Palace and the HTTP UI:

```sh
beemo up --profile garnetmoon
```

The default Garnetmoon profile uses GTE ModernBERT FP16 on Garnetmoon's second
GPU.

The lifecycle command reuses these manifests directly:

- `beemo-project/docker-compose.yaml`
- `beemo-project/docker-compose.gpu.yaml`
- `beemo-project/docker-compose.reranker.garnetmoon.yaml`
- `beemo-project/docker-compose.reranker.gte-modernbert-gpu.yaml`
- `memory_palace/docker-compose.yaml`

It starts services in dependency order:

1. reasoning, embedding, and reranker
2. Memory Palace
3. orchestrator
4. HTTP UI
5. optional voice services

Useful commands:

```sh
beemo status
beemo doctor
beemo restart memory_palace
beemo logs --tail 100 memory_palace
beemo down
```

Use `--memory=false`, `--ui=false`, or `--voice=true` to change optional
features. Set `BEEMO_ROOT` and `MEMORY_PALACE_ROOT` when running outside the
workspace layout.
