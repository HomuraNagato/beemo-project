package main

import (
	"encoding/json"
	"strings"
	"time"

	pb "eve-beemo/proto/gen/proto"
)

func (s *orchestratorServer) StreamState(req *pb.StateRequest, stream pb.Orchestrator_StreamStateServer) error {
	sessionID := strings.TrimSpace(req.GetSessionId())
	updates := make(chan *pb.StateUpdate, 64)

	s.stateMu.Lock()
	if s.stateSubscribers == nil {
		s.stateSubscribers = make(map[uint64]stateSubscriber)
	}
	s.stateSubscriberID++
	id := s.stateSubscriberID
	s.stateSubscribers[id] = stateSubscriber{sessionID: sessionID, updates: updates}
	s.stateMu.Unlock()

	defer func() {
		s.stateMu.Lock()
		delete(s.stateSubscribers, id)
		s.stateMu.Unlock()
	}()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case update := <-updates:
			if err := stream.Send(update); err != nil {
				return err
			}
		}
	}
}

func (s *orchestratorServer) publishState(sessionID, state, message string, timestampUnixMs int64) {
	s.publishStateUpdate(&pb.StateUpdate{
		SessionId: sessionID, State: state, Message: message, TimestampUnixMs: timestampUnixMs,
	})
}

func (s *orchestratorServer) publishStatePayload(sessionID, state, message string, payload any) {
	body, _ := json.Marshal(payload)
	s.publishStateUpdate(&pb.StateUpdate{
		SessionId: sessionID, State: state, Message: message, TimestampUnixMs: time.Now().UnixMilli(), PayloadJson: string(body),
	})
}

func (s *orchestratorServer) publishStateUpdate(update *pb.StateUpdate) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	for _, subscriber := range s.stateSubscribers {
		if subscriber.sessionID != "" && subscriber.sessionID != update.GetSessionId() {
			continue
		}
		select {
		case subscriber.updates <- update:
		default:
			s.log().Warn("orch.state.drop", "session", update.GetSessionId(), "state", update.GetState())
		}
	}
}
