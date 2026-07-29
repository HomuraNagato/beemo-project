package lifecycle

import (
	"slices"
	"testing"
)

func TestResolveProfileUsesGTEOnGarnetmoon(t *testing.T) {
	profile, err := ResolveProfile("garnetmoon")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docker-compose.yaml", "docker-compose.gpu.yaml", "docker-compose.reranker.garnetmoon.yaml", "docker-compose.reranker.gte-modernbert-gpu.yaml"}
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
