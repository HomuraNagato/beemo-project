package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"google.golang.org/grpc"
)

type toolsServer struct {
	pb.UnimplementedToolsServer
}

func (s *toolsServer) Execute(ctx context.Context, req *pb.ToolRequest) (*pb.ToolResult, error) {
	action := req.GetAction()
	switch action {
	case "get_time":
		now := time.Now().Format(time.RFC3339)
		result := &pb.ToolResult{
			Action: action,
			Result: now,
		}
		fmt.Printf("tools.exec action=%s value=%q result=%q\n", action, req.GetValue(), result.Result)
		return result, nil
	default:
		// Echo fallback for quick testing.
		result := fmt.Sprintf("action=%s value=%s", action, req.GetValue())
		resp := &pb.ToolResult{
			Action: action,
			Result: result,
		}
		fmt.Printf("tools.exec action=%s value=%q result=%q\n", action, req.GetValue(), resp.Result)
		return resp, nil
	}
}

func main() {
	addr := os.Getenv("TOOLS_LISTEN_ADDR")
	if addr == "" {
		addr = ":5015"
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("eve-tools: listen error addr=%s err=%v\n", addr, err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterToolsServer(grpcServer, &toolsServer{})

	fmt.Printf("eve-tools: listening addr=%s\n", addr)
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Printf("eve-tools: serve error err=%v\n", err)
		os.Exit(1)
	}
}
