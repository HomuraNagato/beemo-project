package factsel

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"eve-beemo/src/orchestrator/embedding"
	"gopkg.in/yaml.v3"
)

type Fact struct {
	ID              string   `yaml:"id"`
	Kind            string   `yaml:"kind"`
	Summary         string   `yaml:"summary"`
	QuestionLabel   string   `yaml:"question_label"`
	OutputLabel     string   `yaml:"output_label"`
	ExampleRequests []string `yaml:"example_requests"`
}

type Catalog struct {
	Facts []Fact `yaml:"facts"`
}

type Selector struct {
	path    string
	httpURL string
	model   string
	embedFn func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error)

	mu         sync.Mutex
	contentSig [32]byte
	facts      map[string]Fact
	order      []string
	embeddings map[string][]float32
}

const (
	minFactSelectionScore  = 0.50
	minFactSelectionMargin = 0.03
)

func NewSelector(path, httpURL, model string) *Selector {
	return &Selector{
		path:       strings.TrimSpace(path),
		httpURL:    strings.TrimSpace(httpURL),
		model:      strings.TrimSpace(model),
		embedFn:    embedding.Call,
		facts:      map[string]Fact{},
		embeddings: map[string][]float32{},
	}
}

func (s *Selector) Configured() bool {
	return s != nil && s.path != ""
}

func (s *Selector) Enabled() bool {
	return s.Configured() && s.httpURL != ""
}

func (s *Selector) Warmup(timeout time.Duration) error {
	if !s.Configured() {
		return nil
	}
	order, err := s.ensureLoaded()
	if err != nil {
		return err
	}
	if !s.Enabled() {
		return nil
	}
	_, err = s.ensureEmbeddings(order, timeout)
	return err
}

func (s *Selector) Attrs() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func (s *Selector) Fact(attr string) (Fact, bool) {
	if s == nil {
		return Fact{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fact, ok := s.facts[strings.TrimSpace(attr)]
	return fact, ok
}

func (s *Selector) QuestionPrompt(attrs []string) string {
	allowed := s.allowedAttrs(attrs)
	if len(allowed) == 0 {
		return "What fact should I look up?"
	}
	labels := make([]string, 0, len(allowed))
	for _, attr := range allowed {
		if fact, ok := s.Fact(attr); ok {
			label := strings.TrimSpace(fact.QuestionLabel)
			if label == "" {
				label = strings.ReplaceAll(attr, "_", " ")
			}
			labels = append(labels, label)
			continue
		}
		labels = append(labels, strings.ReplaceAll(attr, "_", " "))
	}
	switch len(labels) {
	case 0:
		return "What fact should I look up?"
	case 1:
		return "Should I look up the " + labels[0] + "?"
	default:
		return "What fact should I look up: " + joinLabels(labels) + "?"
	}
}

func (s *Selector) Select(query string, attrs []string, timeout time.Duration) (string, error) {
	if !s.Enabled() {
		return "", nil
	}
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return "", nil
	}
	allowed := s.allowedAttrs(attrs)
	if len(allowed) == 0 {
		return "", nil
	}
	allowed, err := s.ensureEmbeddings(allowed, timeout)
	if err != nil {
		return "", err
	}

	queryVectors, err := s.embedFn(s.httpURL, s.model, []string{queryInstruction(trimmedQuery)}, timeout)
	if err != nil {
		return "", err
	}
	if len(queryVectors) == 0 || len(queryVectors[0]) == 0 {
		return "", fmt.Errorf("fact selector query embedding response: no data")
	}
	queryEmbedding := queryVectors[0]

	s.mu.Lock()
	defer s.mu.Unlock()

	bestAttr := ""
	bestScore := float32(-2)
	secondScore := float32(-2)
	for _, attr := range allowed {
		score := cosineSimilarity(queryEmbedding, s.embeddings[attr])
		if score > bestScore {
			secondScore = bestScore
			bestScore = score
			bestAttr = attr
			continue
		}
		if score > secondScore {
			secondScore = score
		}
	}
	if bestScore < minFactSelectionScore {
		return "", nil
	}
	if secondScore > -2 && bestScore-secondScore < minFactSelectionMargin {
		return "", nil
	}
	return bestAttr, nil
}

func (s *Selector) allowedAttrs(attrs []string) []string {
	s.mu.Lock()
	loaded := len(s.order) > 0
	s.mu.Unlock()
	if !loaded {
		if _, err := s.ensureLoaded(); err != nil {
			return nil
		}
	}

	normalized := normalizeAttrs(attrs)
	if len(normalized) > 0 {
		return normalized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func (s *Selector) ensureLoaded() ([]string, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	sig := sha256.Sum256(data)

	s.mu.Lock()
	if sig == s.contentSig && len(s.order) > 0 {
		order := append([]string(nil), s.order...)
		s.mu.Unlock()
		return order, nil
	}
	s.mu.Unlock()

	var catalog Catalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}

	facts := map[string]Fact{}
	order := make([]string, 0, len(catalog.Facts))
	for _, fact := range catalog.Facts {
		fact.ID = strings.TrimSpace(fact.ID)
		if fact.ID == "" {
			continue
		}
		fact.Kind = strings.TrimSpace(fact.Kind)
		fact.Summary = strings.TrimSpace(fact.Summary)
		fact.QuestionLabel = strings.TrimSpace(fact.QuestionLabel)
		fact.OutputLabel = strings.TrimSpace(fact.OutputLabel)
		if fact.QuestionLabel == "" {
			fact.QuestionLabel = strings.ReplaceAll(fact.ID, "_", " ")
		}
		if fact.OutputLabel == "" {
			fact.OutputLabel = fact.QuestionLabel
		}
		facts[fact.ID] = fact
		order = append(order, fact.ID)
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("facts catalog is empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.contentSig = sig
	s.facts = facts
	s.order = order
	for attr := range s.embeddings {
		if _, ok := facts[attr]; !ok {
			delete(s.embeddings, attr)
		}
	}
	return append([]string(nil), order...), nil
}

func (s *Selector) ensureEmbeddings(attrs []string, timeout time.Duration) ([]string, error) {
	normalized := normalizeAttrs(attrs)
	if len(normalized) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	missing := make([]string, 0, len(normalized))
	for _, attr := range normalized {
		if len(s.embeddings[attr]) == 0 {
			missing = append(missing, attr)
		}
	}
	s.mu.Unlock()

	if len(missing) == 0 {
		return normalized, nil
	}

	inputs := make([]string, 0, len(missing))
	for _, attr := range missing {
		fact, ok := s.Fact(attr)
		if !ok {
			return nil, fmt.Errorf("fact selector missing catalog entry for %s", attr)
		}
		inputs = append(inputs, descriptorForFact(fact))
	}
	vectors, err := s.embedFn(s.httpURL, s.model, inputs, timeout)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(missing) {
		return nil, fmt.Errorf("fact selector embeddings mismatch: got %d want %d", len(vectors), len(missing))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, attr := range missing {
		s.embeddings[attr] = cloneEmbedding(vectors[i])
	}
	return normalized, nil
}

func normalizeAttrs(attrs []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		attr = strings.TrimSpace(attr)
		if attr == "" {
			continue
		}
		if _, ok := seen[attr]; ok {
			continue
		}
		seen[attr] = struct{}{}
		normalized = append(normalized, attr)
	}
	sort.Strings(normalized)
	return normalized
}

func descriptorForFact(f Fact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fact: %s\n", f.ID)
	if f.Summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", f.Summary)
	}
	if f.QuestionLabel != "" {
		fmt.Fprintf(&b, "Question label: %s\n", f.QuestionLabel)
	}
	if len(f.ExampleRequests) > 0 {
		fmt.Fprintf(&b, "Examples:\n")
		for _, item := range f.ExampleRequests {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}
	return strings.TrimSpace(b.String())
}

func queryInstruction(query string) string {
	return "Instruct: Given a user fact recall request, retrieve the fact attribute the user wants to know.\nQuery: " + query
}

func joinLabels(labels []string) string {
	switch len(labels) {
	case 0:
		return ""
	case 1:
		return labels[0]
	case 2:
		return labels[0] + " or " + labels[1]
	default:
		return strings.Join(labels[:len(labels)-1], ", ") + ", or " + labels[len(labels)-1]
	}
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return -1
	}
	var dot float64
	var magA float64
	var magB float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		magA += af * af
		magB += bf * bf
	}
	if magA == 0 || magB == 0 {
		return -1
	}
	return float32(dot / (math.Sqrt(magA) * math.Sqrt(magB)))
}

func cloneEmbedding(input []float32) []float32 {
	if len(input) == 0 {
		return nil
	}
	cloned := make([]float32, len(input))
	copy(cloned, input)
	return cloned
}
