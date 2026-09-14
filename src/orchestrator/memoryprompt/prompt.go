package memoryprompt

import (
	"fmt"
	"strings"
)

type Source struct {
	Title     string
	UpdatedAt string
	SourceURI string
	Evidence  string
}

func BuildAnswer(question string, sources []Source) string {
	return build(
		question,
		sources,
		"If the context is insufficient, say what is missing.",
		"Answer at the depth requested, with citations.",
	)
}

func BuildPlanning(question string, sources []Source) string {
	return build(
		question,
		sources,
		"If the context is insufficient, do not answer or explain what is missing.",
		"If the supplied context is insufficient to answer, reply only with `RETRIEVE: ` followed by one standalone follow-up search query that could locate the missing evidence. Otherwise answer normally, at the depth requested, with citations.",
	)
}

func build(question string, sources []Source, insufficientInstruction, finalInstruction string) string {
	parts := []string{
		"System:\nYou are Beemo. Answer only from the provided Memory Context. Read every numbered source before answering. Use the smallest set of sources sufficient to answer the question. Every factual claim in the answer must be directly supported by its cited sources. Prefer close paraphrases of the supplied evidence. Do not add implications, consequences, examples, interpretations, or background knowledge unless the supplied context states them. When asked why something is important or significant, give only the reason supplied by the context and do not extrapolate broader effects. Shared names, words, topics, or similar situations do not establish that separate sources concern the same subject. Use a source only when it directly supports a claim or when other evidence explicitly connects it to the subject. Combine evidence across sources when genuinely needed, including aliases, pronouns, family relationships, and indirect references. For comparisons, contrast only properties supported for each subject; do not assert an additional relationship between the subjects unless the context states it. First identify the subject of the user's question, then verify that any relationship evidence belongs to that subject. Treat first-person questions using I, me, or my as questions about the user; prefer direct first-person memory evidence over analogous events involving fictional characters. Do not answer with a relation for a different person unless the context links that person back to the asked subject. Cite only the bracketed source numbers shown before each source title, like [1] or [1][3], and never return a citation without an answer. Before returning the answer, remove every claim that is not entailed by the cited context. " + insufficientInstruction + " Match the answer depth to the question: answer direct factual questions in one or two sentences and stop once the requested fact and essential support are given; use several concise paragraphs when the user asks for an explanation, description, comparison, or synthesis.",
		"User:\nQuestion: " + strings.TrimSpace(question),
		"Memory Context:",
	}
	for index, source := range sources {
		parts = append(parts, fmt.Sprintf("[%d] %s\nUpdated: %s\nSource: %s\n%s", index+1, source.Title, source.UpdatedAt, source.SourceURI, source.Evidence))
	}
	parts = append(parts, finalInstruction)
	return strings.Join(parts, "\n\n")
}
