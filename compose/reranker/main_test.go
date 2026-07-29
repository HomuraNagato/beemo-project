package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestBuildSubwordPairTemplates(t *testing.T) {
	query := []int{10, 11}
	passage := []int{20, 21}

	bert := buildSubwordPair(query, passage, 1, 2, "bert")
	wantBert := []int64{1, 10, 11, 2, 20, 21, 2}
	assertInt64Slice(t, bert, wantBert)

	roberta := buildSubwordPair(query, passage, 1, 2, "roberta")
	wantRoberta := []int64{1, 10, 11, 2, 2, 20, 21, 2}
	assertInt64Slice(t, roberta, wantRoberta)
}

func TestTokenizerVocabularySizeIncludesAddedTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokenizer.json")
	if err := os.WriteFile(path, []byte(`{"added_tokens":[{"id":12},{"id":15}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	size, err := tokenizerVocabularySize(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if size != 16 {
		t.Fatalf("expected vocabulary size 16, got %d", size)
	}
}

func TestModernBERTTokenizerMatchesHuggingFace(t *testing.T) {
	path := filepath.Join("..", "..", "models", "reranker", "gte-reranker-modernbert-base", "tokenizer.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("GTE ModernBERT tokenizer is not installed")
	}
	tokenizer, err := NewHuggingFaceTokenizer(path, 50283)
	if err != nil {
		t.Fatal(err)
	}
	ids, _, _, err := tokenizer.EncodePair(
		"What is rank nullity?",
		"The rank plus nullity equals the dimension.",
		384,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{50281, 1276, 310, 5958, 3635, 414, 32, 50282, 510, 5958, 5043, 3635, 414, 18207, 253, 7877, 15, 50282}
	assertInt64Slice(t, ids, want)
}

func assertInt64Slice(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("expected %#v, got %#v", want, got)
		}
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
