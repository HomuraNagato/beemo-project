package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const DefaultProfile = "garnetmoon"

type Profile struct {
	Name         string
	ComposeFiles []string
	LocalDB      bool
}

type Paths struct {
	BeemoRoot        string
	MemoryPalaceRoot string
}

func ResolvePaths(beemoRoot, memoryRoot string) (Paths, error) {
	if strings.TrimSpace(beemoRoot) == "" {
		beemoRoot = os.Getenv("BEEMO_ROOT")
	}
	if strings.TrimSpace(beemoRoot) == "" {
		var err error
		beemoRoot, err = findBeemoRoot()
		if err != nil {
			return Paths{}, err
		}
	}
	beemoRoot, err := filepath.Abs(beemoRoot)
	if err != nil {
		return Paths{}, err
	}
	if strings.TrimSpace(memoryRoot) == "" {
		memoryRoot = os.Getenv("MEMORY_PALACE_ROOT")
	}
	if strings.TrimSpace(memoryRoot) == "" {
		memoryRoot = filepath.Join(filepath.Dir(beemoRoot), "memory_palace")
	}
	memoryRoot, err = filepath.Abs(memoryRoot)
	if err != nil {
		return Paths{}, err
	}
	return Paths{BeemoRoot: beemoRoot, MemoryPalaceRoot: memoryRoot}, nil
}

func findBeemoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "docker-compose.yaml")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("cannot locate Beemo root; run inside beemo-project or set BEEMO_ROOT")
}

func ResolveProfile(name string) (Profile, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = DefaultProfile
	}
	base := []string{"docker-compose.yaml"}
	switch name {
	case "garnetmoon", "vllm-gpu":
		return Profile{Name: name, ComposeFiles: append(base, "docker-compose.gpu.yaml", "docker-compose.reranker.garnetmoon.yaml", "docker-compose.reranker.gte-modernbert-gpu.yaml")}, nil
	case "legion-go", "llama-cpu":
		return Profile{Name: name, ComposeFiles: append(base, "docker-compose.cpu.yaml", "docker-compose.cpu.llamacpp.yaml", "docker-compose.reranker.legion-go.yaml"), LocalDB: true}, nil
	case "vllm-cpu":
		return Profile{Name: name, ComposeFiles: append(base, "docker-compose.cpu.yaml", "docker-compose.cpu.vllm.yaml", "docker-compose.reranker.legion-go.yaml"), LocalDB: true}, nil
	default:
		return Profile{}, fmt.Errorf("unknown profile %q; expected garnetmoon, legion-go, vllm-gpu, vllm-cpu, or llama-cpu", name)
	}
}

func (p Profile) ComposeArgs() []string {
	args := []string{"compose"}
	for _, file := range p.ComposeFiles {
		args = append(args, "-f", file)
	}
	return args
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
