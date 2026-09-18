package main

import (
	"context"
	"strings"
	"time"

	orchtools "eve-beemo/src/orchestrator/tools"
)

const coreContextCacheTTL = time.Minute

func (s *orchestratorServer) coreContext(ctx context.Context) string {
	userKey := strings.TrimSpace(s.cfg.MemoryUserKey)
	if userKey == "" || strings.TrimSpace(s.cfg.MemoryBaseURL) == "" {
		return ""
	}
	now := time.Now()
	s.coreContextMu.Lock()
	cached, ok := s.coreContextCache[userKey]
	if ok && now.Sub(cached.fetchedAt) < coreContextCacheTTL {
		s.coreContextMu.Unlock()
		return cached.body
	}
	s.coreContextMu.Unlock()

	timeoutMs := s.cfg.MemoryTimeoutMs
	if timeoutMs <= 0 || timeoutMs > 2000 {
		timeoutMs = 2000
	}
	result, err := orchtools.GetCoreContext(ctx, orchtools.MemoryConfig{
		BaseURL: s.cfg.MemoryBaseURL, UserKey: userKey, TimeoutMs: timeoutMs,
	})
	if err != nil {
		s.log().Warn("orch.core_context", "user", userKey, "status", "unavailable", "err", err)
		body := ""
		if ok {
			body = cached.body
		}
		s.coreContextMu.Lock()
		if s.coreContextCache == nil {
			s.coreContextCache = map[string]cachedCoreContext{}
		}
		s.coreContextCache[userKey] = cachedCoreContext{body: body, fetchedAt: now}
		s.coreContextMu.Unlock()
		return body
	}
	body := truncateCoreContext(strings.TrimSpace(result.Body), s.cfg.MemoryCoreContextMaxChars)
	s.coreContextMu.Lock()
	if s.coreContextCache == nil {
		s.coreContextCache = map[string]cachedCoreContext{}
	}
	s.coreContextCache[userKey] = cachedCoreContext{body: body, fetchedAt: now}
	s.coreContextMu.Unlock()
	return body
}

func truncateCoreContext(value string, limit int) string {
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
