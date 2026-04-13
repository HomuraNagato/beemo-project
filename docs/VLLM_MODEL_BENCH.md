# vLLM Model Bench

Use `scripts/vllm-model-bench.sh` to compare local and Hugging Face models against the current `eve-vllm` stack.

What it does:
- optionally downloads missing models with `hf download`
- rewrites `REASONING_MODEL` and `LLM_MODEL` in `.env`
- recreates `eve-vllm` and `eve-orchestrator`
- tails `eve-vllm` logs during startup
- waits for the model server to answer
- runs a small set of probes through the existing local scripts
- writes a TSV report under `memory/`
  - if `memory/` is not writable in the current shell path, it falls back to `/tmp/vllm-model-bench/`

Examples:
```bash
./scripts/vllm-model-bench.sh --include-local
./scripts/vllm-model-bench.sh --model Qwen/Qwen2.5-1.5B-Instruct --model Qwen/Qwen2.5-1.5B-Instruct-GPTQ-Int4 --download-missing
./scripts/vllm-model-bench.sh --include-local --include-recommended --download-missing
```

Notes:
- By default, the script restores `.env` when it exits.
- Restoring `.env` does not restart containers back to the original model. Use `--keep-last` if you want the last tested model to remain active in both `.env` and the running stack.
- The current probes are:
  - simple chat response
  - minimal grammar compliance
  - tool-call grammar compliance
  - current-time chat
  - relative-date chat
