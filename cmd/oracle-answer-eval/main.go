package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"eve-beemo/src/orchestrator/llm"
	"eve-beemo/src/orchestrator/memoryprompt"
	"gopkg.in/yaml.v3"
)

type suite struct {
	Cases []evalCase `yaml:"cases"`
}

type evalCase struct {
	ID       string         `yaml:"id" json:"id"`
	Category string         `yaml:"category" json:"category"`
	Question string         `yaml:"question" json:"question"`
	Sources  []evalSource   `yaml:"sources" json:"sources"`
	Expected expectedAnswer `yaml:"expected" json:"expected"`
}

type evalSource struct {
	Title     string `yaml:"title" json:"title"`
	SourceURI string `yaml:"source_uri" json:"source_uri"`
	Evidence  string `yaml:"evidence" json:"evidence"`
}

type expectedAnswer struct {
	AllOf              []string `yaml:"all_of" json:"all_of,omitempty"`
	AnyOf              []string `yaml:"any_of" json:"any_of,omitempty"`
	NoneOf             []string `yaml:"none_of" json:"none_of,omitempty"`
	RequireCitation    bool     `yaml:"require_citation" json:"require_citation"`
	RequiredCitations  []int    `yaml:"required_citations" json:"required_citations,omitempty"`
	ForbiddenCitations []int    `yaml:"forbidden_citations" json:"forbidden_citations,omitempty"`
}

type runResult struct {
	caseID  string
	passed  bool
	latency time.Duration
	reasons []string
	answer  string
}

type runRecord struct {
	Type      string   `json:"type"`
	Timestamp string   `json:"timestamp"`
	Model     string   `json:"model"`
	URL       string   `json:"url"`
	MaxTokens int      `json:"max_tokens"`
	Case      evalCase `json:"case"`
	Run       int      `json:"run"`
	Repeats   int      `json:"repeats"`
	Prompt    string   `json:"prompt"`
	Passed    bool     `json:"passed"`
	LatencyMs int64    `json:"latency_ms"`
	Reasons   []string `json:"reasons,omitempty"`
	Answer    string   `json:"answer"`
}

type summaryRecord struct {
	Type            string         `json:"type"`
	Timestamp       string         `json:"timestamp"`
	Model           string         `json:"model"`
	Cases           int            `json:"cases"`
	Repeats         int            `json:"repeats"`
	Passed          int            `json:"passed"`
	Total           int            `json:"total"`
	ConsistentCases int            `json:"consistent_cases"`
	AverageMs       int64          `json:"average_ms"`
	P50Ms           int64          `json:"p50_ms"`
	P95Ms           int64          `json:"p95_ms"`
	CasePasses      map[string]int `json:"case_passes"`
}

type reportWriter struct {
	path   string
	file   *os.File
	writer *bufio.Writer
}

var latexFormattingPattern = regexp.MustCompile(`\\(?:mathbf|mathrm|mathit|mathbb|text|operatorname)\s*\{([^{}]*)\}`)

func main() {
	file := flag.String("file", "eval/oracle_answer_eval.yaml", "oracle evaluation fixture")
	baseURL := flag.String("url", "http://127.0.0.1:5014/v1/chat/completions", "OpenAI-compatible chat-completions URL")
	model := flag.String("model", "Qwen3-4B-Instruct-2507", "served model name")
	repeats := flag.Int("repeat", 3, "runs per case")
	caseFilter := flag.String("case", "", "comma-separated case IDs")
	list := flag.Bool("list", false, "list selected cases without calling the model")
	output := flag.String("output", "", "JSONL report path (default: timestamped file under history/evals)")
	maxTokens := flag.Int("max-tokens", 384, "maximum answer tokens")
	timeout := flag.Duration("timeout", 2*time.Minute, "timeout per model call")
	flag.Parse()

	loaded, err := loadSuite(*file)
	if err != nil {
		fatal(err)
	}
	selected, err := selectCases(loaded.Cases, *caseFilter)
	if err != nil {
		fatal(err)
	}
	if *list {
		for _, current := range selected {
			fmt.Printf("%s\t%s\t%s\n", current.ID, current.Category, current.Question)
		}
		return
	}
	if *repeats <= 0 {
		fatal(fmt.Errorf("repeat must be positive"))
	}
	report, err := openReport(*output, time.Now())
	if err != nil {
		fatal(err)
	}
	fmt.Printf("report=%s\n", report.path)

	results := make([]runResult, 0, len(selected)*(*repeats))
	for _, current := range selected {
		prompt := memoryprompt.BuildPlanning(current.Question, promptSources(current.Sources))
		for run := 1; run <= *repeats; run++ {
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), *timeout)
			answer, callErr := llm.CallOnceWithMaxTokensContext(ctx, *baseURL, *model, prompt, *maxTokens, *timeout)
			cancel()
			result := score(current, answer, callErr, time.Since(started))
			results = append(results, result)
			if err := report.write(runRecord{
				Type: "run", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Model: *model, URL: *baseURL,
				MaxTokens: *maxTokens, Case: current, Run: run, Repeats: *repeats, Prompt: prompt,
				Passed: result.passed, LatencyMs: result.latency.Milliseconds(), Reasons: result.reasons, Answer: result.answer,
			}); err != nil {
				_ = report.close()
				fatal(err)
			}
			fmt.Printf("case=%s run=%d/%d status=%s latency_ms=%d", current.ID, run, *repeats, passLabel(result.passed), result.latency.Milliseconds())
			if len(result.reasons) > 0 {
				fmt.Printf(" reasons=%q", strings.Join(result.reasons, "; "))
			}
			fmt.Println()
			if !result.passed {
				fmt.Printf("answer=%q\n", result.answer)
			}
		}
	}
	summary := summarize(results, len(selected), *repeats, *model)
	printSummary(summary)
	if err := report.write(summary); err != nil {
		_ = report.close()
		fatal(err)
	}
	if err := report.close(); err != nil {
		fatal(err)
	}
	fmt.Printf("report_complete=%s\n", report.path)
	if !allPassed(results) {
		os.Exit(1)
	}
}

func loadSuite(path string) (suite, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return suite{}, err
	}
	var result suite
	if err := yaml.Unmarshal(body, &result); err != nil {
		return suite{}, err
	}
	if len(result.Cases) == 0 {
		return suite{}, fmt.Errorf("no cases in %s", path)
	}
	seen := map[string]bool{}
	for index, current := range result.Cases {
		if strings.TrimSpace(current.ID) == "" || strings.TrimSpace(current.Question) == "" || len(current.Sources) == 0 {
			return suite{}, fmt.Errorf("case %d requires id, question, and sources", index+1)
		}
		if seen[current.ID] {
			return suite{}, fmt.Errorf("duplicate case id %q", current.ID)
		}
		seen[current.ID] = true
		for _, citation := range current.Expected.RequiredCitations {
			if citation < 1 || citation > len(current.Sources) {
				return suite{}, fmt.Errorf("case %q requires invalid citation [%d]", current.ID, citation)
			}
		}
		for _, citation := range current.Expected.ForbiddenCitations {
			if citation < 1 || citation > len(current.Sources) {
				return suite{}, fmt.Errorf("case %q forbids invalid citation [%d]", current.ID, citation)
			}
		}
	}
	return result, nil
}

func selectCases(cases []evalCase, filter string) ([]evalCase, error) {
	requested := map[string]bool{}
	for _, id := range strings.Split(filter, ",") {
		if id = strings.TrimSpace(id); id != "" {
			requested[id] = true
		}
	}
	if len(requested) == 0 {
		return cases, nil
	}
	selected := make([]evalCase, 0, len(requested))
	for _, current := range cases {
		if requested[current.ID] {
			selected = append(selected, current)
			delete(requested, current.ID)
		}
	}
	if len(requested) > 0 {
		missing := make([]string, 0, len(requested))
		for id := range requested {
			missing = append(missing, id)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("unknown cases: %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func promptSources(sources []evalSource) []memoryprompt.Source {
	result := make([]memoryprompt.Source, 0, len(sources))
	for _, source := range sources {
		result = append(result, memoryprompt.Source{Title: source.Title, SourceURI: source.SourceURI, Evidence: source.Evidence})
	}
	return result
}

func score(current evalCase, answer string, callErr error, latency time.Duration) runResult {
	result := runResult{caseID: current.ID, latency: latency, answer: strings.TrimSpace(answer)}
	if callErr != nil {
		result.reasons = append(result.reasons, "model error: "+callErr.Error())
		return result
	}
	for _, phrase := range current.Expected.AllOf {
		if !containsExpectedPhrase(result.answer, phrase) {
			result.reasons = append(result.reasons, "missing required phrase: "+phrase)
		}
	}
	if len(current.Expected.AnyOf) > 0 {
		matched := false
		for _, phrase := range current.Expected.AnyOf {
			matched = matched || containsExpectedPhrase(result.answer, phrase)
		}
		if !matched {
			result.reasons = append(result.reasons, "missing every alternative: "+strings.Join(current.Expected.AnyOf, " | "))
		}
	}
	for _, phrase := range current.Expected.NoneOf {
		if containsExpectedPhrase(result.answer, phrase) {
			result.reasons = append(result.reasons, "contains forbidden phrase: "+phrase)
		}
	}
	if current.Expected.RequireCitation && !hasCitation(result.answer, len(current.Sources)) {
		result.reasons = append(result.reasons, "missing valid source citation")
	}
	for _, citation := range current.Expected.RequiredCitations {
		marker := fmt.Sprintf("[%d]", citation)
		if !strings.Contains(result.answer, marker) {
			result.reasons = append(result.reasons, "missing required citation: "+marker)
		}
	}
	for _, citation := range current.Expected.ForbiddenCitations {
		marker := fmt.Sprintf("[%d]", citation)
		if strings.Contains(result.answer, marker) {
			result.reasons = append(result.reasons, "cites distractor source: "+marker)
		}
	}
	if strings.HasPrefix(strings.ToUpper(result.answer), "RETRIEVE:") {
		result.reasons = append(result.reasons, "requested retrieval despite oracle evidence")
	}
	result.passed = len(result.reasons) == 0
	return result
}

func containsExpectedPhrase(answer, phrase string) bool {
	answer, phrase = strings.ToLower(answer), strings.ToLower(phrase)
	if strings.Contains(answer, phrase) {
		return true
	}
	normalizedAnswer, normalizedPhrase := normalizeMatchText(answer), normalizeMatchText(phrase)
	return normalizedPhrase != "" && strings.Contains(normalizedAnswer, normalizedPhrase)
}

func normalizeMatchText(value string) string {
	for latexFormattingPattern.MatchString(value) {
		value = latexFormattingPattern.ReplaceAllString(value, "$1")
	}
	var normalized strings.Builder
	for _, current := range value {
		if current >= 'a' && current <= 'z' || current >= '0' && current <= '9' || strings.ContainsRune("=<>+-", current) {
			normalized.WriteRune(current)
		}
	}
	return normalized.String()
}

func hasCitation(answer string, sourceCount int) bool {
	for index := 1; index <= sourceCount; index++ {
		if strings.Contains(answer, fmt.Sprintf("[%d]", index)) {
			return true
		}
	}
	return false
}

func summarize(results []runResult, cases, repeats int, model string) summaryRecord {
	passed := 0
	latencies := make([]time.Duration, 0, len(results))
	casePasses := map[string]int{}
	for _, result := range results {
		if _, exists := casePasses[result.caseID]; !exists {
			casePasses[result.caseID] = 0
		}
		if result.passed {
			passed++
			casePasses[result.caseID]++
		}
		latencies = append(latencies, result.latency)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	total := time.Duration(0)
	for _, latency := range latencies {
		total += latency
	}
	avg, p50, p95 := time.Duration(0), time.Duration(0), time.Duration(0)
	if len(latencies) > 0 {
		avg = total / time.Duration(len(latencies))
		p50 = percentile(latencies, 0.50)
		p95 = percentile(latencies, 0.95)
	}
	consistent := 0
	for _, count := range casePasses {
		if count == repeats {
			consistent++
		}
	}
	return summaryRecord{
		Type: "summary", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Model: model,
		Cases: cases, Repeats: repeats, Passed: passed, Total: len(results), ConsistentCases: consistent,
		AverageMs: avg.Milliseconds(), P50Ms: p50.Milliseconds(), P95Ms: p95.Milliseconds(), CasePasses: casePasses,
	}
}

func printSummary(summary summaryRecord) {
	fmt.Printf("summary model=%q cases=%d repeats=%d passed=%d/%d consistent_cases=%d/%d avg_ms=%d p50_ms=%d p95_ms=%d\n", summary.Model, summary.Cases, summary.Repeats, summary.Passed, summary.Total, summary.ConsistentCases, summary.Cases, summary.AverageMs, summary.P50Ms, summary.P95Ms)
}

func percentile(sorted []time.Duration, percentile float64) time.Duration {
	index := int(float64(len(sorted)-1)*percentile + 0.5)
	return sorted[index]
}

func allPassed(results []runResult) bool {
	for _, result := range results {
		if !result.passed {
			return false
		}
	}
	return true
}

func passLabel(passed bool) string {
	if passed {
		return "pass"
	}
	return "fail"
}

func openReport(path string, now time.Time) (*reportWriter, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = filepath.Join("history", "evals", "oracle-answer-eval-"+now.UTC().Format("20060102T150405Z")+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create report directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	return &reportWriter{path: path, file: file, writer: bufio.NewWriter(file)}, nil
}

func (report *reportWriter) write(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	if _, err := report.writer.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	if err := report.writer.Flush(); err != nil {
		return fmt.Errorf("flush report: %w", err)
	}
	return nil
}

func (report *reportWriter) close() error {
	if report == nil || report.file == nil {
		return nil
	}
	if err := report.writer.Flush(); err != nil {
		_ = report.file.Close()
		return err
	}
	err := report.file.Close()
	report.file = nil
	return err
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "oracle-answer-eval:", err)
	os.Exit(2)
}
