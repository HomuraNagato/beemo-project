package codetools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const DefaultRuntimeDir = "/tmp/beemo-code"

type Config struct {
	Socket       string
	Roots        []string
	MaxOutput    int
	MaxReadBytes int
	CommandTTL   int
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Socket:       codeSocketFromEnvironment(),
		MaxOutput:    256 * 1024,
		MaxReadBytes: 256 * 1024,
		CommandTTL:   120,
	}
	if raw := strings.TrimSpace(os.Getenv("BEEMO_CODE_ROOTS")); raw != "" {
		cfg.Roots = splitRoots(raw)
	} else if cwd, err := os.Getwd(); err == nil {
		cfg.Roots = []string{cwd}
	}
	roots, err := canonicalRoots(cfg.Roots)
	if err != nil {
		return Config{}, err
	}
	cfg.Roots = roots
	return cfg, nil
}

func codeSocketFromEnvironment() string {
	if socket := strings.TrimSpace(os.Getenv("BEEMO_CODE_SOCKET")); socket != "" {
		return socket
	}
	runtimeDir := envOrDefault("BEEMO_CODE_RUNTIME_DIR", DefaultRuntimeDir)
	return filepath.Join(runtimeDir, "beemo-code.sock")
}

func splitRoots(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ':' })
}

func canonicalRoots(roots []string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, fmt.Errorf("workspace root %s: %w", absolute, err)
		}
		if !seen[resolved] {
			seen[resolved] = true
			result = append(result, resolved)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("BEEMO_CODE_ROOTS contains no usable roots")
	}
	sort.Strings(result)
	return result, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
