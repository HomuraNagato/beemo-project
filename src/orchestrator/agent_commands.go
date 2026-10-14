package main

import (
	"context"
	"strings"

	pb "eve-beemo/proto/gen/proto"
)

type agentCommandDefinition struct {
	Command      string
	Action       string
	Description  string
	Discoverable bool
	AcceptsValue bool
}

var agentCommandRegistry = []agentCommandDefinition{
	{Command: "/code", Action: "code", Description: "Enter code mode.", Discoverable: true},
	{Command: "/normal", Action: "normal", Description: "Return to normal mode.", Discoverable: true},
	{Command: "/research", Action: "research", Description: "Research one question with iterative evidence gathering.", Discoverable: true, AcceptsValue: true},
	{Command: "/switch", Action: "switch", Description: "Choose a code project.", Discoverable: true, AcceptsValue: true},
	{Command: "/internet", Action: "internet", Description: "Toggle internet search for this session.", Discoverable: true},
}

func parseSlashCommand(input string) (agentControl, bool) {
	trimmed := strings.TrimSpace(input)
	lower := strings.ToLower(trimmed)
	for _, definition := range agentCommandRegistry {
		command := strings.ToLower(definition.Command)
		if lower == command {
			return agentControl{action: definition.Action}, true
		}
		if definition.AcceptsValue && strings.HasPrefix(lower, command+" ") {
			return agentControl{action: definition.Action, target: strings.TrimSpace(trimmed[len(definition.Command)+1:])}, true
		}
	}
	return agentControl{}, false
}

func (s *orchestratorServer) ListAgentCommands(context.Context, *pb.CommandListRequest) (*pb.CommandListResponse, error) {
	response := &pb.CommandListResponse{Commands: []*pb.AgentCommand{}}
	for _, definition := range agentCommandRegistry {
		if !definition.Discoverable {
			continue
		}
		description := definition.Description
		if definition.Action == "internet" {
			if message := internetConfigurationMessage(s.cfg.InternetProvider, s.cfg.InternetAPIKey); message != "" {
				description = "Internet search unavailable: configure TAVILY_API_KEY."
			}
		}
		response.Commands = append(response.Commands, &pb.AgentCommand{
			Command: definition.Command, Description: description,
		})
	}
	return response, nil
}
