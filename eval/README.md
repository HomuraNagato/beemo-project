# Beemo evaluations

## Oracle answer evaluation

This evaluation bypasses retrieval and supplies fixed answer-bearing evidence to the same planning prompt and LLM client used by Beemo's memory-answer path. It measures answer comprehension, grounding, citation behavior, repeat consistency, and latency.

List cases without contacting a model:

```sh
go run ./cmd/oracle-answer-eval -list
```

Run the complete suite three times against the local reasoning service:

```sh
go run ./cmd/oracle-answer-eval \
  -url http://127.0.0.1:5014/v1/chat/completions \
  -model Qwen3-4B-Instruct-2507 \
  -repeat 3
```

Each run writes a timestamped JSONL report under `history/evals/`. The report contains every answer, including passing answers, together with the exact prompt, oracle evidence, expectations, scoring reasons, model configuration, and latency. Use `-output PATH` to choose a different report file.

Run one case or change the served model:

```sh
go run ./cmd/oracle-answer-eval \
  -case scholomance_els_mother \
  -model SERVED_MODEL_NAME \
  -repeat 5
```

The command exits with status 1 when any run fails and prints failed answers for diagnosis. A case passes only when required facts and a valid citation are present, forbidden contradictions are absent, and the model does not request another retrieval despite having oracle evidence.

## Noisy-context oracle evaluation

The noisy suite keeps the answer-bearing evidence fixed but adds realistic, topically similar passages from other chapters, books, or concepts. It also checks that answers cite required evidence and do not cite explicitly irrelevant distractors.

```sh
go run ./cmd/oracle-answer-eval \
  -file eval/noisy_oracle_answer_eval.yaml \
  -url http://127.0.0.1:5014/v1/chat/completions \
  -model Qwen3-4B-Instruct-2507 \
  -repeat 3
```
