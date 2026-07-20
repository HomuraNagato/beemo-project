# Reranker

`eve-reranker` runs cross-encoder models through ONNX Runtime. The service has
WordPiece and SentencePiece tokenizers and supports ONNX graphs with or without
`token_type_ids`. Compose overrides select the model and machine-specific tuning.

## Profiles

Garnetmoon uses BGE v2-m3 FP16 on GPU 1 as its default:

```sh
docker compose \
  -f docker-compose.yaml \
  -f docker-compose.reranker.garnetmoon.yaml \
  -f docker-compose.reranker.bge.yaml \
  -f docker-compose.reranker.bge-gpu.yaml \
  up -d eve-reranker
```

The BGE v2-m3 FP16 CUDA profile runs on Garnetmoon's GPU 1:

```sh
beemo up --profile garnetmoon-bge
```

The previous INT8 CPU comparison remains available as
`beemo up --profile garnetmoon-bge-cpu`.

Legion Go (Ryzen Z1 Extreme starting profile):

```sh
docker compose \
  -f docker-compose.yaml \
  -f docker-compose.reranker.legion-go.yaml \
  up -d eve-reranker
```

The Legion profile retains MiniLM as the low-latency baseline. It uses a smaller
batch and eight ONNX intra-op threads to limit padding, memory pressure, and
oversubscription. Benchmark BGE on the device before changing that profile.

## Runtime controls

| Variable | Purpose |
| --- | --- |
| `RERANKER_MODEL_PATH` | ONNX graph loaded by the service. |
| `RERANKER_MODEL_URL` | Source used when the graph is absent locally. |
| `RERANKER_TOKENIZER` | `wordpiece` or `sentencepiece`. |
| `RERANKER_TOKENIZER_PATH` | Matching vocabulary or tokenizer artifact. |
| `RERANKER_TOKENIZER_URL` | Source used when the tokenizer is absent locally. |
| `RERANKER_TOKENIZER_BOS_ID` | Beginning-of-sequence token for SentencePiece models. |
| `RERANKER_TOKENIZER_EOS_ID` | End-of-sequence token for SentencePiece models. |
| `RERANKER_TOKENIZER_PAD_ID` | Padding token for SentencePiece models. |
| `RERANKER_INPUT_NAMES` | Ordered comma-separated ONNX input names. |
| `RERANKER_EXECUTION_PROVIDER` | `cpu` or `cuda`. |
| `RERANKER_DEVICE_ID` | Device ordinal visible inside the container. |
| `RERANKER_BATCH_SIZE` | Number of length-sorted query/passage pairs per ONNX call. |
| `RERANKER_MAX_LENGTH` | Hard truncation ceiling for each pair. |
| `RERANKER_INTRA_OP_THREADS` | Threads within ONNX graph operations; `0` lets ONNX choose. |
| `RERANKER_INTER_OP_THREADS` | Threads across graph operations; keep at `1` for this sequential model. |

The service uses dynamic padding and groups candidates by encoded length. Logs
include actual tokens, padded tokens, longest batch sequence, tokenization
time, and ONNX inference time.

## Garnetmoon baseline

Benchmark: retired relationship diagnostic. Active hold-out questions and
answers are intentionally kept outside the repository.

| Implementation | Retrieval | Final rerank | Total | Answer source |
| --- | ---: | ---: | ---: | ---: |
| Fixed 512-token padding | 5.36 s | 1.57 s | 6.93 s | 8 |
| Dynamic padding | 1.73 s | 1.44 s | 3.17 s | 8 |
| Dynamic padding and length bucketing | 1.22 s | 0.98 s | 2.20 s | 8 |

The optimized top-50 evaluation remains `20/20` document recall and `18/20`
evidence recall (`11/13` hard and `7/7` easy).

## BGE v2-m3 CPU trial

The known answer passage ranked 3-4 when inserted into the same 50-candidate
pool, a substantial improvement over MiniLM. CPU latency was not usable for the
current two-stage pipeline: 383 hierarchy candidates took 15.5 seconds, and a
448-candidate request exceeded Memory Palace's 45-second reranker timeout. The
final 50-candidate pass ranged from 8.2 to 35.6 seconds depending on concurrent
pipeline load. Keep this profile for GPU execution work and controlled quality
comparisons rather than normal CPU use.

## BGE v2-m3 GPU trial

Garnetmoon runs the FP16 graph through ONNX Runtime CUDA on physical GPU 1
(RTX 3080 Ti). With batch 16, the fixed 51-candidate comparison took 489 ms
and kept the known answer passage at rank 3. The full retrieval path reranked
384 hierarchy candidates in 677 ms and the final 50 in 368 ms, for 1.46 seconds
end to end. The service used about 2.37 GiB of GPU memory. Batch 32 was slightly
slower at 502 ms and used about 3.4 GiB, so batch 16 remains the default.

The remaining failed relationship cases did not include their answer passages
in the final candidate pool. That is now a retrieval candidate-selection issue,
not reranker latency or ranking once the passage is present.
