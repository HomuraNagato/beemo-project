package lifecycle

import (
	"slices"
	"testing"
)

func TestResolveProfileReusesGarnetmoonRerankerOverride(t *testing.T) {
	profile, err := ResolveProfile("garnetmoon")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker-compose.yaml", "docker-compose.gpu.yaml", "docker-compose.reranker.garnetmoon.yaml", "docker-compose.reranker.bge.yaml", "docker-compose.reranker.bge-gpu.yaml"}
	if !slices.Equal(profile.ComposeFiles, want) {
		t.Fatalf("unexpected compose files: %#v", profile.ComposeFiles)
	}
}

func TestResolveProfileReusesLegionRerankerOverride(t *testing.T) {
	profile, err := ResolveProfile("legion-go")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(profile.ComposeFiles, "docker-compose.reranker.legion-go.yaml") ||
		!slices.Contains(profile.ComposeFiles, "docker-compose.cpu.llamacpp.yaml") {
		t.Fatalf("unexpected compose files: %#v", profile.ComposeFiles)
	}
}

func TestResolveProfileAddsBGERerankerLast(t *testing.T) {
	profile, err := ResolveProfile("garnetmoon-bge")
	if err != nil {
		t.Fatal(err)
	}
	want := "docker-compose.reranker.bge-gpu.yaml"
	if got := profile.ComposeFiles[len(profile.ComposeFiles)-1]; got != want {
		t.Fatalf("expected final override %q, got %q", want, got)
	}
}

func TestResolveProfileKeepsBGECPUComparison(t *testing.T) {
	profile, err := ResolveProfile("garnetmoon-bge-cpu")
	if err != nil {
		t.Fatal(err)
	}
	want := "docker-compose.reranker.bge.yaml"
	if got := profile.ComposeFiles[len(profile.ComposeFiles)-1]; got != want {
		t.Fatalf("expected final override %q, got %q", want, got)
	}
}
