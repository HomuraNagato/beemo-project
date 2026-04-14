# vLLM With Hugging Face Models

Use a Hugging Face model directory or repo ID with vLLM instead of GGUF when possible. This is the cleaner path for OpenAI-compatible serving and structured output work.

## Recommended Env

Set these in `.env`:

```env
REASONING_HOST=0.0.0.0
REASONING_PORT=5014
MODEL_DIR=/models
REASONING_MODEL=Qwen2.5-7B-Instruct
LLM_MODEL=Qwen2.5-7B-Instruct
VLLM_TENSOR_PARALLEL_SIZE=1
VLLM_GPU_MEMORY_UTILIZATION=0.90
VLLM_MAX_MODEL_LEN=8192
VLLM_CPU_OFFLOAD_GB=6
VLLM_SWAP_SPACE_GB=8
LLM_HTTP_URL=http://eve-vllm:5014/v1/chat/completions
```

By default, the vLLM entrypoint derives the load path as:

```text
${MODEL_DIR}/${REASONING_MODEL}
```

You can override that by setting `REASONING_MODEL_PATH`.

## Local Model Download

The simplest stable approach is to download the model into `./models` on the host.

Example with `huggingface_hub`:

```bash
python3 -m pip install -U huggingface_hub
huggingface-cli download Qwen/Qwen2.5-7B-Instruct \
  --local-dir ./models/Qwen2.5-7B-Instruct
```

If the model is gated, first authenticate:

```bash
huggingface-cli login
```

## Direct Download In Container

You can also set:

```env
REASONING_MODEL=Qwen/Qwen2.5-7B-Instruct
HF_TOKEN=...
```

This is convenient, but local pre-download is easier to inspect and cache in this repo layout.

## Suggested First Models

Start with an instruct model that is known to behave well with structured output prompts. Reasonable first options:
- `Qwen/Qwen2.5-7B-Instruct`
- `Qwen/Qwen2.5-14B-Instruct`
- `meta-llama/Llama-3.1-8B-Instruct`

## Bringing It Up

After updating `.env`:

```bash
docker compose -f docker-compose.yaml -f docker-compose.gpu.yaml up -d eve-vllm
```

Then test:

```bash
./scripts/llama-chat.sh --docker --host http://eve-vllm:5014 --prompt 'Say hello in one word.'
./scripts/llama-complete.sh --docker --host http://eve-vllm:5014 --prompt 'Return ["ok"] exactly.' --grammar-file scripts/grammars/min_array.gbnf
```

## Notes

- `vllm/vllm-openai:latest` is used directly in `docker-compose.gpu.yaml`; no Dockerfile is required initially.
- `compose/reasoning_vllm/entrypoint.sh` is the runtime launcher.
- The API model name is `REASONING_MODEL`.
- The filesystem load path defaults to `${MODEL_DIR}/${REASONING_MODEL}` unless `REASONING_MODEL_PATH` is set.
- If GPU memory is tight, reduce `VLLM_MAX_MODEL_LEN` or choose a smaller model.
- If VRAM is still tight, increase `VLLM_CPU_OFFLOAD_GB` and `VLLM_SWAP_SPACE_GB` carefully and expect lower throughput.
