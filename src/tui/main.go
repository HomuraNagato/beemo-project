package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultTimeout = 2 * time.Minute

type transcriptEntry struct {
	role    string
	content string
}

func main() {
	addr := flag.String("addr", getenvOrDefault("ORCH_ADDR", "localhost:5013"), "orchestrator gRPC address")
	sessionID := flag.String("session", getenvOrDefault("SESSION_ID", "tui"), "chat session id")
	timeout := flag.Duration("timeout", defaultTimeout, "request timeout")
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
				content: "Commands: /help, /clear, /faces, /face <emotion>, /quit",
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
		}

		transcript = append(transcript, transcriptEntry{role: "user", content: line})
		messages = append(messages, &pb.ChatMessage{Role: "user", Content: line})
		currentExpression = expressionForEmotion("thinking")
		render(*addr, *sessionID, currentExpression, transcript)

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		resp, err := client.Chat(ctx, &pb.ChatRequest{
			SessionId: *sessionID,
			Messages:  messages,
		})
		cancel()

		if err != nil {
			currentExpression = expressionForEmotion("error")
			transcript = append(transcript, transcriptEntry{
				role:    "system",
				content: fmt.Sprintf("request failed: %v", err),
			})
			render(*addr, *sessionID, currentExpression, transcript)
			continue
		}

		reply := strings.TrimSpace(resp.GetText())
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
	fmt.Println("commands: /help /clear /faces /face <emotion> /quit")
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

func getenvOrDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}
