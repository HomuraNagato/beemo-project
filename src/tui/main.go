package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultTimeout = 15 * time.Minute

type transcriptEntry struct {
	role    string
	content string
}

func main() {
	addr := flag.String("addr", "127.0.0.1:5013", "orchestrator gRPC address")
	sessionID := flag.String("session", "tui", "chat session id")
	timeout := flag.Duration("timeout", defaultTimeout, "request timeout")
	mode := flag.String("mode", "chat", "session mode: chat or code")
	workspace := flag.String("workspace", "", "repository path for Code mode")
	resume := flag.Bool("resume", false, "resume the persisted session id")
	flag.Parse()

	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewOrchestratorClient(conn)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)
	baseSessionID := *sessionID

	var transcript []transcriptEntry
	var messages []*pb.ChatMessage
	currentExpression := defaultExpression()
	if *resume {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		detail, resumeErr := client.GetAgentSession(ctx, &pb.SessionRequest{SessionId: *sessionID})
		cancel()
		if resumeErr != nil {
			fmt.Fprintf(os.Stderr, "resume error: %v\n", resumeErr)
		} else {
			*mode = detail.GetSession().GetMode()
			*workspace = detail.GetSession().GetWorkspace()
			for _, event := range detail.GetEvents() {
				if event.GetType() != "user" && event.GetType() != "assistant" {
					continue
				}
				content := strings.TrimSpace(event.GetText())
				messages = append(messages, &pb.ChatMessage{Role: event.GetType(), Content: content})
				transcript = append(transcript, transcriptEntry{role: event.GetType(), content: stripExpressionTag(content)})
			}
		}
	}

	render(*addr, *sessionID, currentExpression, transcript)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		}

		switch {
		case line == "/quit" || line == "/exit":
			return
		case line == "/clear":
			transcript = nil
			messages = nil
			currentExpression = defaultExpression()
			*sessionID = fmt.Sprintf("%s-%d", baseSessionID, time.Now().UnixNano())
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		case line == "/help":
			transcript = append(transcript, transcriptEntry{
				role:    "system",
				content: "Commands: /mode chat|code, /workspace <path>, /help, /clear, /faces, /face <emotion>, /quit",
			})
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		case line == "/faces":
			transcript = append(transcript, transcriptEntry{
				role:    "system",
				content: faceList(),
			})
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		case strings.HasPrefix(line, "/face "):
			emotion := strings.TrimSpace(strings.TrimPrefix(line, "/face "))
			nextExpression := expressionForEmotion(emotion)
			if nextExpression.Emotion != normalizeEmotion(emotion) {
				transcript = append(transcript, transcriptEntry{
					role:    "system",
					content: fmt.Sprintf("Unknown expression %q. %s", emotion, faceList()),
				})
				render(*addr, *sessionID, currentExpression, transcript)
				continue
			}
			currentExpression = nextExpression
			transcript = append(transcript, transcriptEntry{
				role:    "system",
				content: fmt.Sprintf("Expression set to %s.", currentExpression.Emotion),
			})
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		case strings.HasPrefix(line, "/mode "):
			next := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "/mode ")))
			if next != "chat" && next != "code" {
				transcript = append(transcript, transcriptEntry{role: "system", content: "Mode must be chat or code."})
			} else {
				*mode = next
				transcript = append(transcript, transcriptEntry{role: "system", content: "Mode set to " + next + "."})
			}
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		case strings.HasPrefix(line, "/workspace "):
			*workspace = strings.TrimSpace(strings.TrimPrefix(line, "/workspace "))
			transcript = append(transcript, transcriptEntry{role: "system", content: "Workspace set to " + *workspace + "."})
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		}

		transcript = append(transcript, transcriptEntry{role: "user", content: line})
		messages = append(messages, &pb.ChatMessage{Role: "user", Content: line})
		currentExpression = expressionForEmotion("thinking")
		render(*addr, *sessionID, currentExpression, transcript)

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		stream, err := client.RunAgent(ctx, &pb.AgentRequest{
			SessionId: *sessionID,
			Messages:  messages,
			Mode:      *mode,
			Workspace: *workspace,
		})
		if err != nil {
			cancel()
			currentExpression = expressionForEmotion("error")
			transcript = append(transcript, transcriptEntry{
				role:    "system",
				content: fmt.Sprintf("request failed: %v", err),
			})
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		}

		reply := ""
		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr != io.EOF {
					transcript = append(transcript, transcriptEntry{role: "system", content: "stream failed: " + recvErr.Error()})
				}
				break
			}
			switch event.GetType() {
			case "assistant":
				reply = strings.TrimSpace(event.GetText())
			case "tool_start":
				transcript = append(transcript, transcriptEntry{role: "tool", content: event.GetTool()})
				render(*addr, *sessionID, currentExpression, transcript)
			case "tool_result":
				text := strings.TrimSpace(event.GetText())
				if len(text) > 360 {
					text = text[:360] + "..."
				}
				if text != "" {
					transcript = append(transcript, transcriptEntry{role: "tool", content: event.GetTool() + ": " + text})
				}
			case "approval":
				fmt.Printf("\nApproval required for %s: %s\nApprove? [y/N] ", event.GetTool(), event.GetText())
				approved := scanner.Scan() && strings.EqualFold(strings.TrimSpace(scanner.Text()), "y")
				approvalCtx, approvalCancel := context.WithTimeout(context.Background(), 10*time.Second)
				_, approvalErr := client.Approve(approvalCtx, &pb.ApprovalDecision{
					SessionId: *sessionID, ApprovalId: event.GetApprovalId(), Approved: approved,
				})
				approvalCancel()
				if approvalErr != nil {
					transcript = append(transcript, transcriptEntry{role: "system", content: "approval failed: " + approvalErr.Error()})
				}
			case "error":
				transcript = append(transcript, transcriptEntry{role: "system", content: event.GetText()})
				currentExpression = expressionForEmotion("error")
			}
		}
		cancel()
		if reply == "" {
			reply = "(empty response)"
		}
		currentExpression = expressionForAssistantReply(reply)
		reply = stripExpressionTag(reply)
		transcript = append(transcript, transcriptEntry{role: "assistant", content: reply})
		messages = append(messages, &pb.ChatMessage{Role: "assistant", Content: reply})
		render(*addr, *sessionID, currentExpression, transcript)
	}
}

func render(addr, sessionID string, expr expression, transcript []transcriptEntry) {
	fmt.Print("\033[H\033[2J")
	fmt.Println("beemo console")
	for _, line := range expr.Face {
		fmt.Println(line)
	}
	fmt.Printf("expression: %s (%s)\n", expr.Emotion, expr.Label)
	fmt.Printf("addr: %s\n", addr)
	fmt.Printf("session: %s\n", sessionID)
	fmt.Println("commands: /mode chat|code /workspace <path> /help /clear /faces /face <emotion> /quit")
	fmt.Println(strings.Repeat("-", 72))
	if len(transcript) == 0 {
		fmt.Println("No messages yet.")
		fmt.Println()
		return
	}

	for _, entry := range transcript {
		fmt.Printf("%s: %s\n\n", strings.ToUpper(entry.role), entry.content)
	}
}

func faceList() string {
	return "Expressions: " + strings.Join(expressionOrder, ", ")
}
