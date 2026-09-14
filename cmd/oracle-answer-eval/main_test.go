package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScoreChecksFactsContradictionsAndCitation(t *testing.T) {
	current := evalCase{ID: "monty", Sources: []evalSource{{Title: "Probability and Computing"}}, Expected: expectedAnswer{
		AllOf: []string{"Monty Hall"}, NoneOf: []string{"no explicitly stated problem"}, RequireCitation: true,
	}}
	if got := score(current, "Exercise 1.12 is the Monty Hall problem [1].", nil, time.Second); !got.passed {
		t.Fatalf("expected pass, got %#v", got.reasons)
	}
	got := score(current, "There is no explicitly stated problem [1].", nil, time.Second)
	if got.passed || len(got.reasons) != 2 {
		t.Fatalf("expected missing fact and contradiction, got %#v", got.reasons)
	}
}

func TestReportWriterPersistsRunAndSummaryRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.jsonl")
	report, err := openReport(path, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := report.write(runRecord{Type: "run", Answer: "Gwen Higgins [1].", Passed: true}); err != nil {
		t.Fatal(err)
	}
	if err := report.write(summaryRecord{Type: "summary", Passed: 1, Total: 1}); err != nil {
		t.Fatal(err)
	}
	if err := report.close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two records, got %d", len(lines))
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSONL record: %v", err)
		}
	}
}

func TestScoreTreatsRetrievalAndModelErrorsAsFailures(t *testing.T) {
	current := evalCase{ID: "answer", Sources: []evalSource{{Title: "Source"}}}
	if got := score(current, "RETRIEVE: another query", nil, 0); got.passed {
		t.Fatal("retrieval request must fail with oracle evidence")
	}
	if got := score(current, "", errors.New("offline"), 0); got.passed {
		t.Fatal("model error must fail")
	}
}

func TestScoreCanRequireEvidenceFromMultipleSources(t *testing.T) {
	current := evalCase{ID: "synthesis", Sources: []evalSource{{Title: "One"}, {Title: "Two"}}, Expected: expectedAnswer{
		RequiredCitations: []int{1, 2},
	}}
	if got := score(current, "Combined answer [1][2].", nil, 0); !got.passed {
		t.Fatalf("expected both citations to pass, got %#v", got.reasons)
	}
	if got := score(current, "Partial answer [1].", nil, 0); got.passed {
		t.Fatal("expected missing second citation to fail")
	}
}

func TestScoreCanRejectDistractorCitations(t *testing.T) {
	current := evalCase{ID: "noise", Sources: []evalSource{{Title: "Evidence"}, {Title: "Distractor"}}, Expected: expectedAnswer{
		RequiredCitations: []int{1}, ForbiddenCitations: []int{2},
	}}
	if got := score(current, "Grounded answer [1].", nil, 0); !got.passed {
		t.Fatalf("expected evidence citation to pass, got %#v", got.reasons)
	}
	if got := score(current, "Mixed answer [1][2].", nil, 0); got.passed {
		t.Fatal("expected distractor citation to fail")
	}
}

func TestScoreNormalizesLatexMathFormatting(t *testing.T) {
	current := evalCase{ID: "math", Sources: []evalSource{{Title: "Linear algebra"}}, Expected: expectedAnswer{
		AllOf: []string{"Ax = 0"},
	}}
	answer := `The nullspace contains every vector \( \mathbf{x} \) satisfying \( A\mathbf{x} = \mathbf{0} \).`
	if got := score(current, answer, nil, 0); !got.passed {
		t.Fatalf("expected equivalent LaTeX math to pass, got %#v", got.reasons)
	}
}
