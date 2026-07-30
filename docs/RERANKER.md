# Reranker

`eve-reranker` runs cross-encoder models through ONNX Runtime. The service has
WordPiece, SentencePiece, Hugging Face, and remote Jina tokenizers and supports
ONNX graphs with or without `token_type_ids`. Compose overrides select the model
and machine-specific tuning.

## Profiles

Garnetmoon uses GTE ModernBERT FP16 on GPU 1 as its default:

```sh
docker compose \
  -f docker-compose.yaml \
  -f docker-compose.reranker.garnetmoon.yaml \
  -f docker-compose.reranker.gte-modernbert-gpu.yaml \
  up -d eve-reranker
```

The equivalent lifecycle command is:

```sh
beemo up --profile garnetmoon
```

Legion Go (Ryzen Z1 Extreme starting profile):

```sh
docker compose \
  -f docker-compose.yaml \
  -f docker-compose.reranker.legion-go.yaml \
  up -d eve-reranker
```

The Legion profile retains MiniLM as the low-latency baseline. It uses a smaller
batch and eight ONNX intra-op threads to limit padding, memory pressure, and
oversubscription.

## Runtime controls

| Variable | Purpose |
| --- | --- |
| `RERANKER_MODEL_PATH` | ONNX graph loaded by the service. |
| `RERANKER_MODEL_URL` | Source used when the graph is absent locally. |
| `RERANKER_TOKENIZER` | `wordpiece`, `sentencepiece`, `huggingface`, or `jina-remote`. |
| `RERANKER_TOKENIZER_PATH` | Matching vocabulary or tokenizer artifact. |
| `RERANKER_TOKENIZER_URL` | Source used when the tokenizer is absent locally. |
| `RERANKER_TOKENIZER_BOS_ID` | Beginning-of-sequence token for SentencePiece models. |
| `RERANKER_TOKENIZER_EOS_ID` | End-of-sequence token for SentencePiece models. |
| `RERANKER_TOKENIZER_PAD_ID` | Padding token for SentencePiece models. |
| `RERANKER_INPUT_NAMES` | Ordered comma-separated ONNX input names. |
| `RERANKER_EXECUTION_PROVIDER` | `cpu` or `cuda`. |
| `RERANKER_DEVICE_ID` | Device ordinal visible inside the container. |
| `RERANKER_GPU_MEMORY_LIMIT_MB` | Optional CUDA arena limit in MiB; `0` leaves it unlimited. |
| `RERANKER_BATCH_SIZE` | Number of length-sorted query/passage pairs per ONNX call. |
| `RERANKER_MAX_LENGTH` | Hard truncation ceiling for each pair. |
| `RERANKER_INTRA_OP_THREADS` | Threads within ONNX graph operations; `0` lets ONNX choose. |
| `RERANKER_INTER_OP_THREADS` | Threads across graph operations; keep at `1` for this sequential model. |

The service uses dynamic padding and groups candidates by encoded length. Logs
include actual tokens, padded tokens, longest batch sequence, tokenization
time, and ONNX inference time.

## Garnetmoon baseline

The July 30, 2026 comparison reranked the same fixed top-50 Memory Palace
candidate shape and measured evidence recall at eight results:

| Model | Ranking method | Evidence recall | Average rerank | GPU process |
| --- | --- | ---: | ---: | ---: |
| GTE ModernBERT FP16 | pairwise cross-encoder | 17/28 | 127 ms | 1,432 MiB |
| Qwen3 Reranker 0.6B | pairwise cross-encoder | 16/28 | 117 ms | 4,244 MiB |
| Jina Reranker v3.5 | listwise, 50 documents | 7/28 | 159 ms | 8,432 MiB |

GTE remains the Garnetmoon default. Qwen did not improve recall and required
about three times GTE's GPU allocation. Jina received all 50 passages in one
listwise request, but ranked this mixed personal-memory, fiction, and textbook
corpus substantially worse.

The benchmark profiles are isolated from the production service:

```sh
docker compose -f docker-compose.reranker.qwen06-benchmark.yaml up -d --build
docker compose -f docker-compose.reranker.jina35-benchmark.yaml up -d --build
```

Run them one at a time on GPU 1. Both expose the standard Memory Palace
`/rerank` contract through the reusable Go proxy.

The evaluation suite's corpus audit validates all 28 answer-bearing source and
evidence anchors. Exhaustive source reranking is not a fair listwise comparison:
18 expected sources contain more than 150 indexed passages, while production
always supplies a bounded 50-passage pool.
