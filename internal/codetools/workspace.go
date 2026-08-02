package codetools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Resolver struct {
	roots []string
}

func NewResolver(roots []string) Resolver {
	return Resolver{roots: append([]string(nil), roots...)}
}

func (r Resolver) Roots() []string {
	return append([]string(nil), r.roots...)
}

func (r Resolver) Workspace(path string) (string, error) {
	return r.resolve(path, false)
}

func (r Resolver) ExistingPath(workspace, relative string) (string, error) {
	root, err := r.Workspace(workspace)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(relative) == "" {
		return root, nil
	}
	return r.resolve(filepath.Join(root, relative), false)
}

func (r Resolver) WritablePath(workspace, relative string) (string, error) {
	root, err := r.Workspace(workspace)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	parent := target
	for {
		if _, statErr := os.Lstat(parent); statErr == nil {
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", fmt.Errorf("cannot resolve parent for %s", target)
		}
		parent = next
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	resolvedTarget := filepath.Join(resolvedParent, strings.TrimPrefix(target, parent))
	if !within(root, resolvedTarget) {
		return "", fmt.Errorf("path escapes workspace: %s", relative)
	}
	return target, nil
}

func (r Resolver) resolve(path string, allowMissing bool) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if allowMissing && os.IsNotExist(err) {
			resolved = absolute
		} else {
			return "", err
		}
	}
	for _, root := range r.roots {
		if within(root, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path is outside configured workspace roots: %s", path)
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
