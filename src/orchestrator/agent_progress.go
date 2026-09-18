package main

import (
	"math"
	"strings"

	"eve-beemo/src/orchestrator/llm"
)

const limitWarningThreshold = 0.8

type agentProgress struct {
	Phase            string `json:"phase,omitempty"`
	Step             int    `json:"step,omitempty"`
	MaxSteps         int    `json:"maxSteps,omitempty"`
	PromptTokens     int    `json:"promptTokens,omitempty"`
	CompletionTokens int    `json:"completionTokens,omitempty"`
	ContextLimit     int    `json:"contextLimit,omitempty"`
	OutputLimit      int    `json:"outputLimit,omitempty"`
	Warning          string `json:"warning,omitempty"`
	Incomplete       bool   `json:"incomplete,omitempty"`
}

func progressForCompletion(result llm.Completion, contextLimit, outputLimit int) agentProgress {
	progress := agentProgress{
		PromptTokens: result.Usage.PromptTokens, CompletionTokens: result.Usage.CompletionTokens,
		ContextLimit: contextLimit, OutputLimit: outputLimit,
	}
	switch {
	case strings.EqualFold(result.FinishReason, "length"):
		progress.Warning = "output_limit"
		progress.Incomplete = true
	case contextLimit > 0 && float64(result.Usage.PromptTokens)/float64(contextLimit) >= limitWarningThreshold:
		progress.Warning = "context_near_limit"
	case outputLimit > 0 && float64(result.Usage.CompletionTokens)/float64(outputLimit) >= limitWarningThreshold:
		progress.Warning = "output_near_limit"
	}
	return progress
}

func codeStepProgress(step, maxSteps int) agentProgress {
	progress := agentProgress{Phase: "planning", Step: step, MaxSteps: maxSteps}
	if maxSteps > 0 && step >= int(math.Ceil(float64(maxSteps)*limitWarningThreshold)) {
		progress.Warning = "tool_near_limit"
	}
	return progress
}

func toolActivity(tool string) string {
	switch strings.TrimSpace(tool) {
	case "code.read":
		return "Reading files"
	case "code.search":
		return "Searching files"
	case "code.list":
		return "Listing files"
	case "code.exec":
		return "Running command"
	case "code.git_status", "code.git_diff":
		return "Checking changes"
	case "internet.search":
		return "Searching internet"
	case "memory.ask", "memory.search":
		return "Searching memory"
	case "weather":
		return "Checking weather"
	case "calculator":
		return "Calculating"
	case "older_sister":
		return "Asking Older Sister"
	default:
		return "Running tool"
	}
}
