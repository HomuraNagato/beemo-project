package memoryprompt

import (
	"strings"
	"testing"
)

func TestBuildPlanningIncludesEvidenceAndRetrievalContract(t *testing.T) {
	prompt := BuildPlanning("Who is El's mother?", []Source{{
		Title: "A Deadly Education", Evidence: "Gwen Higgins is your mother.", SourceURI: "book://scholomance",
	}})
	for _, expected := range []string{"Gwen Higgins", "[1] A Deadly Education", "reply only with `RETRIEVE: `"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
}

func TestBuildersIncludeSharedGroundingContract(t *testing.T) {
	source := []Source{{
		Title: "Relevant source", Evidence: "The requested fact.", SourceURI: "memory://relevant",
	}}
	builders := map[string]func(string, []Source) string{
		"answer":   BuildAnswer,
		"planning": BuildPlanning,
	}
	expected := []string{
		"Use the smallest set of sources sufficient to answer the question.",
		"Every factual claim in the answer must be directly supported by its cited sources.",
		"Prefer close paraphrases of the supplied evidence.",
		"Do not add implications, consequences, examples, interpretations, or background knowledge unless the supplied context states them.",
		"When asked why something is important or significant, give only the reason supplied by the context and do not extrapolate broader effects.",
		"Shared names, words, topics, or similar situations do not establish that separate sources concern the same subject.",
		"Use a source only when it directly supports a claim or when other evidence explicitly connects it to the subject.",
		"For comparisons, contrast only properties supported for each subject; do not assert an additional relationship between the subjects unless the context states it.",
		"remove every claim that is not entailed by the cited context.",
		"stop once the requested fact and essential support are given",
	}

	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			prompt := build("What is the requested fact?", source)
			for _, text := range expected {
				if !strings.Contains(prompt, text) {
					t.Fatalf("prompt missing %q:\n%s", text, prompt)
				}
			}
		})
	}
}
