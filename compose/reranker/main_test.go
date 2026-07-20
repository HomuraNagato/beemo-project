package main

import "testing"

func TestEncodePairReturnsCompactSequence(t *testing.T) {
	tokenizer := &WordPieceTokenizer{vocab: map[string]int64{
		"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
		"query": 4, "passage": 5,
	}}

	ids, mask, types, err := tokenizer.EncodePair("query", "passage", 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 5 || len(mask) != 5 || len(types) != 5 {
		t.Fatalf("expected compact five-token pair, got ids=%d mask=%d types=%d", len(ids), len(mask), len(types))
	}
	for _, value := range mask {
		if value != 1 {
			t.Fatalf("expected compact attention mask, got %#v", mask)
		}
	}
}

func TestEncodePairHonorsMaximumLength(t *testing.T) {
	tokenizer := &WordPieceTokenizer{vocab: map[string]int64{
		"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
		"query": 4, "passage": 5,
	}}

	ids, _, _, err := tokenizer.EncodePair("query query", "passage passage passage", 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 6 {
		t.Fatalf("expected sequence truncated to six tokens, got %d", len(ids))
	}
}

func TestTruncatePairUsesLongestFirst(t *testing.T) {
	left := []int{1, 2}
	right := []int{3, 4, 5, 6}
	truncatePair(&left, &right, 4)
	if len(left) != 2 || len(right) != 2 {
		t.Fatalf("expected balanced four-token pair, got left=%v right=%v", left, right)
	}
}

func TestConfigSupportsTwoInputModel(t *testing.T) {
	t.Setenv("RERANKER_INPUT_NAMES", "input_ids,attention_mask")
	t.Setenv("RERANKER_EXECUTION_PROVIDER", "CUDA")
	t.Setenv("RERANKER_DEVICE_ID", "1")
	cfg := configFromEnv()
	if len(cfg.InputNames) != 2 || cfg.InputNames[1] != inputNameMask {
		t.Fatalf("unexpected model inputs: %v", cfg.InputNames)
	}
	if cfg.ExecutionProvider != "cuda" || cfg.DeviceID != 1 {
		t.Fatalf("unexpected execution provider config: provider=%q device=%d", cfg.ExecutionProvider, cfg.DeviceID)
	}
}

func TestConfigureExecutionProviderRejectsUnknownProvider(t *testing.T) {
	err := configureExecutionProvider(nil, Config{ExecutionProvider: "unknown"})
	if err == nil {
		t.Fatal("expected unsupported provider error")
	}
}

func TestAppendPaddedUsesRequestedValue(t *testing.T) {
	got := appendPadded([]int64{9}, []int64{1, 2}, 4, 7)
	want := []int64{9, 1, 2, 7, 7}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("expected %#v, got %#v", want, got)
		}
	}
}
