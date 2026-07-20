package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sentencepiece "github.com/tggo/goSentencePiece"
	ort "github.com/yalue/onnxruntime_go"
)

const (
	defaultModelName    = "cross-encoder/ms-marco-MiniLM-L6-v2"
	defaultModelURL     = "https://huggingface.co/cross-encoder/ms-marco-MiniLM-L6-v2/resolve/main/onnx/model.onnx"
	defaultTokenizerURL = "https://huggingface.co/cross-encoder/ms-marco-MiniLM-L6-v2/resolve/main/vocab.txt"
	defaultMaxLength    = 512
	defaultBatchSize    = 8
	defaultPort         = "5022"
	contentTypeJSON     = "application/json"
	inputNameIDs        = "input_ids"
	inputNameMask       = "attention_mask"
	inputNameTypeIDs    = "token_type_ids"
	outputNameLogits    = "logits"
)

type Config struct {
	ModelName         string
	ModelPath         string
	ModelURL          string
	TokenizerKind     string
	TokenizerPath     string
	TokenizerURL      string
	TokenizerBOSID    int
	TokenizerEOSID    int
	TokenizerPADID    int
	InputNames        []string
	OutputNames       []string
	RuntimeLibrary    string
	ExecutionProvider string
	DeviceID          int
	MaxLength         int
	BatchSize         int
	IntraOpThreads    int
	InterOpThreads    int
	Port              string
}

type Candidate struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Title string `json:"title,omitempty"`
}

type RerankRequest struct {
	RequestID  string      `json:"request_id,omitempty"`
	Query      string      `json:"query"`
	Candidates []Candidate `json:"candidates"`
	TopK       int         `json:"top_k"`
	MaxLength  int         `json:"max_length,omitempty"`
}

type RerankResult struct {
	ID    string  `json:"id"`
	Score float32 `json:"score"`
	Index int     `json:"index"`
}

type RerankResponse struct {
	Model   string         `json:"model"`
	Results []RerankResult `json:"results"`
}

type Server struct {
	cfg       Config
	tokenizer PairTokenizer
	session   *ort.DynamicAdvancedSession
	mu        sync.Mutex
}

func main() {
	cfg := configFromEnv()
	if err := ensureFile(cfg.ModelPath, cfg.ModelURL); err != nil {
		log.Fatalf("model setup failed: %v", err)
	}
	if err := ensureFile(cfg.TokenizerPath, cfg.TokenizerURL); err != nil {
		log.Fatalf("tokenizer artifact setup failed: %v", err)
	}

	ort.SetSharedLibraryPath(cfg.RuntimeLibrary)
	if err := ort.InitializeEnvironment(); err != nil {
		log.Fatalf("onnxruntime init failed: %v", err)
	}
	defer ort.DestroyEnvironment()

	tokenizer, err := newPairTokenizer(cfg)
	if err != nil {
		log.Fatalf("tokenizer setup failed: %v", err)
	}
	sessionOptions, err := ort.NewSessionOptions()
	if err != nil {
		log.Fatalf("onnx session options failed: %v", err)
	}
	defer sessionOptions.Destroy()
	if err := sessionOptions.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll); err != nil {
		log.Fatalf("onnx graph optimization setup failed: %v", err)
	}
	if err := sessionOptions.SetIntraOpNumThreads(cfg.IntraOpThreads); err != nil {
		log.Fatalf("onnx intra-op thread setup failed: %v", err)
	}
	if err := sessionOptions.SetInterOpNumThreads(cfg.InterOpThreads); err != nil {
		log.Fatalf("onnx inter-op thread setup failed: %v", err)
	}
	if err := configureExecutionProvider(sessionOptions, cfg); err != nil {
		log.Fatalf("execution provider setup failed: %v", err)
	}
	session, err := ort.NewDynamicAdvancedSession(
		cfg.ModelPath,
		cfg.InputNames,
		cfg.OutputNames,
		sessionOptions,
	)
	if err != nil {
		log.Fatalf("onnx session setup failed: %v", err)
	}
	defer session.Destroy()

	server := &Server{
		cfg:       cfg,
		tokenizer: tokenizer,
		session:   session,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", server.handleHealth)
	mux.HandleFunc("/rerank", server.handleRerank)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("eve-reranker-go listening on :%s model=%s tokenizer=%s provider=%s device_id=%d inputs=%s max_length=%d batch_size=%d intra_op_threads=%d inter_op_threads=%d", cfg.Port, cfg.ModelName, cfg.TokenizerKind, cfg.ExecutionProvider, cfg.DeviceID, strings.Join(cfg.InputNames, ","), cfg.MaxLength, cfg.BatchSize, cfg.IntraOpThreads, cfg.InterOpThreads)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func configFromEnv() Config {
	modelDir := envOrDefault("RERANKER_MODEL_DIR", "/models/reranker/ms-marco-MiniLM-L6-v2")
	return Config{
		ModelName:         envOrDefault("RERANKER_MODEL", defaultModelName),
		ModelPath:         envOrDefault("RERANKER_MODEL_PATH", filepath.Join(modelDir, "model.onnx")),
		ModelURL:          envOrDefault("RERANKER_MODEL_URL", defaultModelURL),
		TokenizerKind:     envOrDefault("RERANKER_TOKENIZER", "wordpiece"),
		TokenizerPath:     envOrDefault("RERANKER_TOKENIZER_PATH", filepath.Join(modelDir, "vocab.txt")),
		TokenizerURL:      envOrDefault("RERANKER_TOKENIZER_URL", defaultTokenizerURL),
		TokenizerBOSID:    envNonNegativeInt("RERANKER_TOKENIZER_BOS_ID", 0),
		TokenizerEOSID:    envNonNegativeInt("RERANKER_TOKENIZER_EOS_ID", 2),
		TokenizerPADID:    envNonNegativeInt("RERANKER_TOKENIZER_PAD_ID", 1),
		InputNames:        envCSV("RERANKER_INPUT_NAMES", []string{inputNameIDs, inputNameMask, inputNameTypeIDs}),
		OutputNames:       envCSV("RERANKER_OUTPUT_NAMES", []string{outputNameLogits}),
		RuntimeLibrary:    envOrDefault("ONNXRUNTIME_SHARED_LIBRARY_PATH", "/opt/onnxruntime/lib/libonnxruntime.so.1.26.0"),
		ExecutionProvider: strings.ToLower(envOrDefault("RERANKER_EXECUTION_PROVIDER", "cpu")),
		DeviceID:          envNonNegativeInt("RERANKER_DEVICE_ID", 0),
		MaxLength:         envInt("RERANKER_MAX_LENGTH", defaultMaxLength),
		BatchSize:         envInt("RERANKER_BATCH_SIZE", defaultBatchSize),
		IntraOpThreads:    envNonNegativeInt("RERANKER_INTRA_OP_THREADS", 0),
		InterOpThreads:    envNonNegativeInt("RERANKER_INTER_OP_THREADS", 1),
		Port:              envOrDefault("RERANKER_PORT", defaultPort),
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"model":     s.cfg.ModelName,
		"runtime":   "go-onnxruntime",
		"provider":  s.cfg.ExecutionProvider,
		"device_id": s.cfg.DeviceID,
	})
}

func configureExecutionProvider(options *ort.SessionOptions, cfg Config) error {
	switch cfg.ExecutionProvider {
	case "cpu":
		return nil
	case "cuda":
		cudaOptions, err := ort.NewCUDAProviderOptions()
		if err != nil {
			return err
		}
		defer cudaOptions.Destroy()
		if err := cudaOptions.Update(map[string]string{
			"device_id":                 strconv.Itoa(cfg.DeviceID),
			"do_copy_in_default_stream": "1",
			"use_tf32":                  "1",
		}); err != nil {
			return err
		}
		return options.AppendExecutionProviderCUDA(cudaOptions)
	default:
		return fmt.Errorf("unsupported execution provider %q", cfg.ExecutionProvider)
	}
}

func (s *Server) handleRerank(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var req RerankRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.RequestID == "" {
		req.RequestID = strings.TrimSpace(r.Header.Get("X-Request-ID"))
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is required"})
		return
	}
	if len(req.Candidates) == 0 {
		writeJSON(w, http.StatusOK, RerankResponse{Model: s.cfg.ModelName, Results: []RerankResult{}})
		return
	}
	topK := req.TopK
	if topK <= 0 || topK > len(req.Candidates) {
		topK = len(req.Candidates)
	}
	maxLength := req.MaxLength
	if maxLength <= 0 {
		maxLength = s.cfg.MaxLength
	}

	scores, stats, err := s.score(r.Context(), req.Query, req.Candidates, maxLength)
	if err != nil {
		log.Printf("reranker.rerank request_id=%q status=error query=%q query_chars=%d candidates=%d top_k=%d max_length=%d batches=%d actual_tokens=%d padded_tokens=%d max_batch_length=%d tokenize_ms=%d onnx_ms=%d total_ms=%d err=%q", req.RequestID, req.Query, len([]rune(req.Query)), len(req.Candidates), topK, maxLength, stats.BatchCount, stats.ActualTokens, stats.PaddedTokens, stats.MaxBatchLength, stats.TokenizeMs, stats.ONNXMs, time.Since(start).Milliseconds(), err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	results := make([]RerankResult, 0, len(scores))
	for i, score := range scores {
		results = append(results, RerankResult{
			ID:    req.Candidates[i].ID,
			Score: score,
			Index: i,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	log.Printf("reranker.rerank request_id=%q status=ok query=%q query_chars=%d candidates=%d top_k=%d max_length=%d batches=%d actual_tokens=%d padded_tokens=%d max_batch_length=%d tokenize_ms=%d onnx_ms=%d total_ms=%d", req.RequestID, req.Query, len([]rune(req.Query)), len(req.Candidates), topK, maxLength, stats.BatchCount, stats.ActualTokens, stats.PaddedTokens, stats.MaxBatchLength, stats.TokenizeMs, stats.ONNXMs, time.Since(start).Milliseconds())
	writeJSON(w, http.StatusOK, RerankResponse{
		Model:   s.cfg.ModelName,
		Results: results[:topK],
	})
}

type scoreStats struct {
	BatchCount     int
	ActualTokens   int
	PaddedTokens   int
	MaxBatchLength int
	TokenizeMs     int64
	ONNXMs         int64
}

type encodedPair struct {
	index int
	ids   []int64
	mask  []int64
	types []int64
}

func (s *Server) score(ctx context.Context, query string, candidates []Candidate, maxLength int) ([]float32, scoreStats, error) {
	scores := make([]float32, len(candidates))
	stats := scoreStats{}
	encoded := make([]encodedPair, 0, len(candidates))
	tokenizeStart := time.Now()
	for index, candidate := range candidates {
		passage := strings.TrimSpace(candidate.Text)
		if title := strings.TrimSpace(candidate.Title); title != "" {
			passage = title + "\n" + passage
		}
		ids, mask, types, err := s.tokenizer.EncodePair(query, passage, maxLength)
		if err != nil {
			return nil, stats, err
		}
		encoded = append(encoded, encodedPair{index: index, ids: ids, mask: mask, types: types})
		stats.ActualTokens += len(ids)
	}
	stats.TokenizeMs = time.Since(tokenizeStart).Milliseconds()
	sort.SliceStable(encoded, func(i, j int) bool {
		return len(encoded[i].ids) < len(encoded[j].ids)
	})
	for start := 0; start < len(encoded); start += s.cfg.BatchSize {
		stats.BatchCount++
		end := min(start+s.cfg.BatchSize, len(encoded))
		batch := encoded[start:end]
		batchLength := len(batch[len(batch)-1].ids)
		stats.PaddedTokens += len(batch) * batchLength
		stats.MaxBatchLength = max(stats.MaxBatchLength, batchLength)
		inputIDs := make([]int64, 0, len(batch)*batchLength)
		attentionMask := make([]int64, 0, len(batch)*batchLength)
		tokenTypeIDs := make([]int64, 0, len(batch)*batchLength)
		padID := s.tokenizer.PadID()
		for _, pair := range batch {
			inputIDs = appendPadded(inputIDs, pair.ids, batchLength, padID)
			attentionMask = appendPadded(attentionMask, pair.mask, batchLength, 0)
			tokenTypeIDs = appendPadded(tokenTypeIDs, pair.types, batchLength, 0)
		}
		onnxStart := time.Now()
		batchScores, err := s.runBatch(ctx, len(batch), batchLength, inputIDs, attentionMask, tokenTypeIDs)
		stats.ONNXMs += time.Since(onnxStart).Milliseconds()
		if err != nil {
			return nil, stats, err
		}
		for index, score := range batchScores {
			scores[batch[index].index] = score
		}
	}
	return scores, stats, nil
}

func appendPadded(destination, values []int64, length int, padding int64) []int64 {
	destination = append(destination, values...)
	for index := len(values); index < length; index++ {
		destination = append(destination, padding)
	}
	return destination
}

func (s *Server) runBatch(ctx context.Context, batchSize, maxLength int, inputIDs, attentionMask, tokenTypeIDs []int64) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	shape := ort.NewShape(int64(batchSize), int64(maxLength))
	inputTensor, err := ort.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, err
	}
	defer inputTensor.Destroy()
	maskTensor, err := ort.NewTensor(shape, attentionMask)
	if err != nil {
		return nil, err
	}
	defer maskTensor.Destroy()

	var typeTensor *ort.Tensor[int64]
	if slicesContain(s.cfg.InputNames, inputNameTypeIDs) {
		typeTensor, err = ort.NewTensor(shape, tokenTypeIDs)
		if err != nil {
			return nil, err
		}
		defer typeTensor.Destroy()
	}
	inputValues := make([]ort.Value, 0, len(s.cfg.InputNames))
	for _, name := range s.cfg.InputNames {
		switch name {
		case inputNameIDs:
			inputValues = append(inputValues, inputTensor)
		case inputNameMask:
			inputValues = append(inputValues, maskTensor)
		case inputNameTypeIDs:
			inputValues = append(inputValues, typeTensor)
		default:
			return nil, fmt.Errorf("unsupported ONNX input %q", name)
		}
	}

	outputs := []ort.Value{nil}
	s.mu.Lock()
	err = s.session.Run(inputValues, outputs)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer outputs[0].Destroy()
	output, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected output tensor type %T", outputs[0])
	}
	raw := output.GetData()
	scores := make([]float32, batchSize)
	for i := range scores {
		if len(raw) == batchSize {
			scores[i] = raw[i]
		} else {
			scores[i] = raw[i*2+1]
		}
		if math.IsNaN(float64(scores[i])) || math.IsInf(float64(scores[i]), 0) {
			scores[i] = 0
		}
	}
	return scores, nil
}

type WordPieceTokenizer struct {
	vocab map[string]int64
}

func NewWordPieceTokenizer(path string) (*WordPieceTokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	vocab := map[string]int64{}
	for i, line := range strings.Split(string(data), "\n") {
		token := strings.TrimSpace(line)
		if token == "" {
			continue
		}
		vocab[token] = int64(i)
	}
	for _, token := range []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]"} {
		if _, ok := vocab[token]; !ok {
			return nil, fmt.Errorf("vocab missing %s", token)
		}
	}
	return &WordPieceTokenizer{vocab: vocab}, nil
}

func (t *WordPieceTokenizer) EncodePair(query, passage string, maxLength int) ([]int64, []int64, []int64, error) {
	if maxLength < 3 {
		return nil, nil, nil, fmt.Errorf("max length must be at least 3 for WordPiece pairs")
	}
	queryPieces := t.tokenize(query)
	passagePieces := t.tokenize(passage)
	available := maxLength - 3
	for len(queryPieces)+len(passagePieces) > available {
		if len(passagePieces) >= len(queryPieces) && len(passagePieces) > 0 {
			passagePieces = passagePieces[:len(passagePieces)-1]
		} else if len(queryPieces) > 0 {
			queryPieces = queryPieces[:len(queryPieces)-1]
		} else {
			break
		}
	}

	ids := make([]int64, 0, maxLength)
	types := make([]int64, 0, maxLength)
	add := func(token string, tokenType int64) {
		ids = append(ids, t.id(token))
		types = append(types, tokenType)
	}
	add("[CLS]", 0)
	for _, token := range queryPieces {
		add(token, 0)
	}
	add("[SEP]", 0)
	for _, token := range passagePieces {
		add(token, 1)
	}
	add("[SEP]", 1)

	mask := make([]int64, len(ids))
	for i := range ids {
		mask[i] = 1
	}
	return ids, mask, types, nil
}

func (t *WordPieceTokenizer) PadID() int64 {
	return t.id("[PAD]")
}

func (t *WordPieceTokenizer) tokenize(text string) []string {
	words := basicTokenize(text)
	pieces := make([]string, 0, len(words))
	for _, word := range words {
		pieces = append(pieces, t.wordPieces(word)...)
	}
	return pieces
}

func (t *WordPieceTokenizer) wordPieces(word string) []string {
	if len([]rune(word)) > 100 {
		return []string{"[UNK]"}
	}
	runes := []rune(word)
	pieces := []string{}
	for start := 0; start < len(runes); {
		end := len(runes)
		var current string
		for start < end {
			part := string(runes[start:end])
			if start > 0 {
				part = "##" + part
			}
			if _, ok := t.vocab[part]; ok {
				current = part
				break
			}
			end--
		}
		if current == "" {
			return []string{"[UNK]"}
		}
		pieces = append(pieces, current)
		start = end
	}
	return pieces
}

func (t *WordPieceTokenizer) id(token string) int64 {
	if id, ok := t.vocab[token]; ok {
		return id
	}
	return t.vocab["[UNK]"]
}

type PairTokenizer interface {
	EncodePair(query, passage string, maxLength int) ([]int64, []int64, []int64, error)
	PadID() int64
}

func newPairTokenizer(cfg Config) (PairTokenizer, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.TokenizerKind)) {
	case "wordpiece":
		return NewWordPieceTokenizer(cfg.TokenizerPath)
	case "sentencepiece":
		return NewSentencePieceTokenizer(cfg.TokenizerPath, cfg.TokenizerBOSID, cfg.TokenizerEOSID, cfg.TokenizerPADID)
	default:
		return nil, fmt.Errorf("unsupported tokenizer %q", cfg.TokenizerKind)
	}
}

type SentencePieceTokenizer struct {
	tokenizer *sentencepiece.Tokenizer
	bosID     int64
	eosID     int64
	padID     int64
}

func NewSentencePieceTokenizer(path string, bosID, eosID, padID int) (*SentencePieceTokenizer, error) {
	tokenizer, err := sentencepiece.NewTokenizer(path)
	if err != nil {
		return nil, err
	}
	for name, id := range map[string]int{"BOS": bosID, "EOS": eosID, "PAD": padID} {
		if id < 0 || id >= tokenizer.VocabSize() {
			return nil, fmt.Errorf("%s token ID %d is outside tokenizer vocabulary", name, id)
		}
	}
	return &SentencePieceTokenizer{
		tokenizer: tokenizer,
		bosID:     int64(bosID),
		eosID:     int64(eosID),
		padID:     int64(padID),
	}, nil
}

func (t *SentencePieceTokenizer) EncodePair(query, passage string, maxLength int) ([]int64, []int64, []int64, error) {
	const specialTokenCount = 4
	if maxLength < specialTokenCount {
		return nil, nil, nil, fmt.Errorf("max length must be at least %d for SentencePiece pairs", specialTokenCount)
	}
	queryTokens, err := t.tokenizer.Encode(query)
	if err != nil {
		return nil, nil, nil, err
	}
	passageTokens, err := t.tokenizer.Encode(passage)
	if err != nil {
		return nil, nil, nil, err
	}
	truncatePair(&queryTokens, &passageTokens, maxLength-specialTokenCount)

	ids := make([]int64, 0, maxLength)
	ids = append(ids, t.bosID)
	ids = appendIntIDs(ids, queryTokens)
	ids = append(ids, t.eosID, t.eosID)
	ids = appendIntIDs(ids, passageTokens)
	ids = append(ids, t.eosID)
	mask := filledInt64(len(ids), 1)
	types := make([]int64, len(ids))
	return ids, mask, types, nil
}

func (t *SentencePieceTokenizer) PadID() int64 {
	return t.padID
}

func truncatePair(left, right *[]int, available int) {
	for len(*left)+len(*right) > available {
		if len(*right) >= len(*left) && len(*right) > 0 {
			*right = (*right)[:len(*right)-1]
		} else if len(*left) > 0 {
			*left = (*left)[:len(*left)-1]
		}
	}
}

func appendIntIDs(destination []int64, values []int) []int64 {
	for _, value := range values {
		destination = append(destination, int64(value))
	}
	return destination
}

func filledInt64(length int, value int64) []int64 {
	result := make([]int64, length)
	for index := range result {
		result[index] = value
	}
	return result
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var tokenPattern = regexp.MustCompile(`[\p{L}\p{N}]+|[^\s\p{L}\p{N}]`)

func basicTokenize(text string) []string {
	text = strings.ToLower(stripControl(text))
	return tokenPattern.FindAllString(text, -1)
}

func stripControl(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, text)
}

func ensureFile(path, url string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	log.Printf("downloading %s -> %s", url, path)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("download failed %s: %s", url, resp.Status)
	}
	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err = io.Copy(file, resp.Body); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("content-type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envNonNegativeInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func envCSV(key string, fallback []string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	if len(result) == 0 {
		return fallback
	}
	return result
}
