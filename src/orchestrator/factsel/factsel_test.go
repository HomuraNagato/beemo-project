package factsel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectorSelectsBestFactAttribute(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "facts.yaml")
	if err := os.WriteFile(path, []byte(`
facts:
  - id: weight
    kind: measurement
    summary: Recall weight.
    question_label: weight
    output_label: weight
    example_requests:
      - what is my weight?
  - id: height
    kind: measurement
    summary: Recall height.
    question_label: height
    output_label: height
    example_requests:
      - how tall is she?
  - id: age_years
    kind: years
    summary: Recall age.
    question_label: age in years
    output_label: age in years
    example_requests:
      - how old is she?
`), 0644); err != nil {
		t.Fatalf("write facts file: %v", err)
	}

	selector := NewSelector(path, "http://embed.test/v1/embeddings", "test-model")
	selector.embedFn = func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error) {
		vectors := make([][]float32, 0, len(inputs))
		for _, input := range inputs {
			switch {
			case strings.Contains(input, "Fact: weight"):
				vectors = append(vectors, []float32{1, 0, 0, 0, 0})
			case strings.Contains(input, "Fact: height"):
				vectors = append(vectors, []float32{0, 1, 0, 0, 0})
			case strings.Contains(input, "Fact: age_years"):
				vectors = append(vectors, []float32{0, 0, 1, 0, 0})
			case strings.Contains(input, "Fact: gender"):
				vectors = append(vectors, []float32{0, 0, 0, 1, 0})
			case strings.Contains(input, "Fact: activity_level"):
				vectors = append(vectors, []float32{0, 0, 0, 0, 1})
			case strings.Contains(input, "how tall is she"):
				vectors = append(vectors, []float32{0, 1, 0, 0, 0})
			default:
				vectors = append(vectors, []float32{0.2, 0.2, 0.2, 0.2, 0.2})
			}
		}
		return vectors, nil
	}
	if err := selector.Warmup(time.Second); err != nil {
		t.Fatalf("Warmup returned error: %v", err)
	}

	attr, err := selector.Select("how tall is she?", []string{"weight", "height", "age_years"}, time.Second)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got, want := attr, "height"; got != want {
		t.Fatalf("unexpected attribute: got %q want %q", got, want)
	}
}
