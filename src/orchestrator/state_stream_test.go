package main

import (
	"context"
	"io"
	"testing"
	"time"

	pb "eve-beemo/proto/gen/proto"
	"google.golang.org/grpc/metadata"
)

type recordingStateStream struct {
	ctx     context.Context
	updates chan *pb.StateUpdate
}

func (s *recordingStateStream) Send(update *pb.StateUpdate) error {
	s.updates <- update
	return nil
}

func (s *recordingStateStream) SetHeader(metadata.MD) error  { return nil }
func (s *recordingStateStream) SendHeader(metadata.MD) error { return nil }
func (s *recordingStateStream) SetTrailer(metadata.MD)       {}
func (s *recordingStateStream) Context() context.Context     { return s.ctx }
func (s *recordingStateStream) SendMsg(any) error            { return nil }
func (s *recordingStateStream) RecvMsg(any) error            { return io.EOF }

func TestStreamStatePublishesOnlyMatchingSession(t *testing.T) {
	server := &orchestratorServer{}
	ctx, cancel := context.WithCancel(context.Background())
	stream := &recordingStateStream{ctx: ctx, updates: make(chan *pb.StateUpdate, 1)}
	done := make(chan error, 1)
	go func() {
		done <- server.StreamState(&pb.StateRequest{SessionId: "voice-loop"}, stream)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		server.stateMu.Lock()
		subscribers := len(server.stateSubscribers)
		server.stateMu.Unlock()
		if subscribers == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("state subscriber was not registered")
		}
		time.Sleep(time.Millisecond)
	}

	server.publishState("other", "user", "ignored", 1)
	server.publishState("voice-loop", "assistant", "hello", 2)
	select {
	case update := <-stream.updates:
		if update.GetState() != "assistant" || update.GetMessage() != "hello" {
			t.Fatalf("unexpected update: %#v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for state update")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("state stream did not stop after cancellation")
	}
}
