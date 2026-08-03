package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const SessionFileName = ".beemo-session"

func SessionPath(paths Paths) string {
	return filepath.Join(paths.BeemoRoot, SessionFileName)
}

func ReadSession(paths Paths) (string, error) {
	raw, err := os.ReadFile(SessionPath(paths))
	if err != nil {
		return "", err
	}
	sessionID := strings.TrimSpace(string(raw))
	if sessionID == "" {
		return "", fmt.Errorf("active Beemo session is empty")
	}
	return sessionID, nil
}

func RotateSession(paths Paths) (string, error) {
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	sessionID := fmt.Sprintf("beemo-%s-%s", time.Now().UTC().Format("20060102T150405Z"), hex.EncodeToString(suffix))
	path := SessionPath(paths)
	if err := os.WriteFile(path, []byte(sessionID+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write active session %s: %w", path, err)
	}
	return sessionID, nil
}
