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

	var transcript []transcriptEntry
	var messages []*pb.ChatMessage

	render(*addr, *sessionID, transcript)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			render(*addr, *sessionID, transcript)
			continue
		}

		switch line {
		case "/quit", "/exit":
			return
		case "/clear":
			transcript = nil
			messages = nil
			render(*addr, *sessionID, transcript)
			continue
		case "/help":
			transcript = append(transcript, transcriptEntry{
				role:    "system",
				content: "Commands: /help, /clear, /quit",
			})
			render(*addr, *sessionID, transcript)
			continue
		}

		transcript = append(transcript, transcriptEntry{role: "user", content: line})
		messages = append(messages, &pb.ChatMessage{Role: "user", Content: line})
		render(*addr, *sessionID, transcript)

		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		resp, err := client.Chat(ctx, &pb.ChatRequest{
			SessionId: *sessionID,
			Messages:  messages,
		})
		cancel()

		if err != nil {
			transcript = append(transcript, transcriptEntry{
				role:    "system",
				content: fmt.Sprintf("request failed: %v", err),
			})
			render(*addr, *sessionID, transcript)
			continue
		}

		reply := strings.TrimSpace(resp.GetText())
		if reply == "" {
			reply = "(empty response)"
		}
		transcript = append(transcript, transcriptEntry{role: "assistant", content: reply})
		messages = append(messages, &pb.ChatMessage{Role: "assistant", Content: reply})
		render(*addr, *sessionID, transcript)
	}
}

func render(addr, sessionID string, transcript []transcriptEntry) {
	fmt.Print("\033[H\033[2J")
	fmt.Println("eve-orchestrator tui")
	fmt.Printf("addr: %s\n", addr)
	fmt.Printf("session: %s\n", sessionID)
	fmt.Println("commands: /help /clear /quit")
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

func getenvOrDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}
