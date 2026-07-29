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

GTE ModernBERT FP16 reached 15/27 evidence recall at eight results on the fixed
top-50 evaluation, compared with BGE's 13/27. Average reranking latency was
116 ms. After warmup, the service used about 802 MiB of GPU memory, approximately
1,572 MiB less than the prior BGE process.
