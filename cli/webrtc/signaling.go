package webrtc

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v3"
)

// SignalingMessage represents a message exchanged during WebRTC signaling
type SignalingMessage struct {
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	PeerToken string `json:"peerToken,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	ICE       string `json:"ice,omitempty"`
}

// MessageSender sends messages to the signaling server
type MessageSender interface {
	SendSignalingMessage(msg SignalingMessage) error
}

// Signaling handles WebRTC signaling
type Signaling struct {
	connection *Connection
	sender     MessageSender
	logger     Logger
}

// NewSignaling creates a new signaling handler
func NewSignaling(
	connection *Connection,
	sender MessageSender,
	logger Logger,
) *Signaling {
	return &Signaling{
		connection: connection,
		sender:     sender,
		logger:     logger,
	}
}

// HandleOffer handles an SDP offer
func (s *Signaling) HandleOffer(msg SignalingMessage) error {
	// Parse the SDP offer
	var offer webrtc.SessionDescription
	err := json.Unmarshal([]byte(msg.SDP), &offer)
	if err != nil {
		return fmt.Errorf("failed to parse SDP offer: %v", err)
	}

	// Set the remote description
	err = s.connection.state.PeerConn.SetRemoteDescription(offer)
	if err != nil {
		return fmt.Errorf("failed to set remote description: %v", err)
	}

	// Create an answer
	answer, err := s.connection.state.PeerConn.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("failed to create answer: %v", err)
	}

	// Set the local description
	err = s.connection.state.PeerConn.SetLocalDescription(answer)
	if err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	// Marshal the answer
	answerObj := struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}{
		Type: answer.Type.String(),
		SDP:  answer.SDP,
	}
	answerJSON, err := json.Marshal(answerObj)
	if err != nil {
		return fmt.Errorf("failed to marshal answer: %v", err)
	}

	// Send the answer
	err = s.sender.SendSignalingMessage(SignalingMessage{
		Type:      "answer",
		PeerToken: msg.Token,
		SDP:       string(answerJSON),
	})
	if err != nil {
		return fmt.Errorf("failed to send answer: %v", err)
	}

	return nil
}

// HandleAnswer handles an SDP answer
func (s *Signaling) HandleAnswer(msg SignalingMessage) error {
	// Parse the SDP answer
	var answer webrtc.SessionDescription
	err := json.Unmarshal([]byte(msg.SDP), &answer)
	if err != nil {
		return fmt.Errorf("failed to parse SDP answer: %v", err)
	}

	// Set the remote description
	err = s.connection.state.PeerConn.SetRemoteDescription(answer)
	if err != nil {
		return fmt.Errorf("failed to set remote description: %v", err)
	}

	return nil
}

// HandleICE handles an ICE candidate
func (s *Signaling) HandleICE(msg SignalingMessage) error {
	// Parse the ICE candidate
	var candidate webrtc.ICECandidateInit
	err := json.Unmarshal([]byte(msg.ICE), &candidate)
	if err != nil {
		return fmt.Errorf("failed to parse ICE candidate: %v", err)
	}

	// Add the ICE candidate
	err = s.connection.state.PeerConn.AddICECandidate(candidate)
	if err != nil {
		return fmt.Errorf("failed to add ICE candidate: %v", err)
	}

	return nil
}

// CreateOffer creates an SDP offer
func (s *Signaling) CreateOffer() error {
	// Create an offer
	offer, err := s.connection.state.PeerConn.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("failed to create offer: %v", err)
	}

	// Set the local description
	err = s.connection.state.PeerConn.SetLocalDescription(offer)
	if err != nil {
		return fmt.Errorf("failed to set local description: %v", err)
	}

	// Marshal the offer
	offerObj := struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}{
		Type: offer.Type.String(),
		SDP:  offer.SDP,
	}
	offerJSON, err := json.Marshal(offerObj)
	if err != nil {
		return fmt.Errorf("failed to marshal offer: %v", err)
	}

	// Send the offer
	err = s.sender.SendSignalingMessage(SignalingMessage{
		Type:      "offer",
		PeerToken: s.connection.state.PeerToken,
		SDP:       string(offerJSON),
	})
	if err != nil {
		return fmt.Errorf("failed to send offer: %v", err)
	}

	return nil
}

// SetupICEHandlers sets up handlers for ICE candidates
func (s *Signaling) SetupICEHandlers() {
	// Set up ICE candidate handler
	s.connection.state.PeerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		// Marshal the ICE candidate
		candidateJSON, err := json.Marshal(candidate.ToJSON())
		if err != nil {
			s.logger.LogDebug(fmt.Sprintf("Failed to marshal ICE candidate: %v", err))
			return
		}

		// Send the ICE candidate
		err = s.sender.SendSignalingMessage(SignalingMessage{
			Type:      "ice",
			PeerToken: s.connection.state.PeerToken,
			ICE:       string(candidateJSON),
		})
		if err != nil {
			s.logger.LogDebug(fmt.Sprintf("Failed to send ICE candidate: %v", err))
			return
		}
	})

	// Set up connection state change handler
	s.connection.state.PeerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		s.logger.LogDebug(fmt.Sprintf("Peer connection state changed: %s", state.String()))

		switch state {
		case webrtc.PeerConnectionStateFailed:
			s.logger.LogDebug("Peer connection failed, disconnecting")
			s.connection.Disconnect()
		case webrtc.PeerConnectionStateClosed:
			s.logger.LogDebug("Peer connection closed")
			s.connection.Disconnect()
		case webrtc.PeerConnectionStateDisconnected:
			s.logger.LogDebug("Peer connection disconnected")
			s.connection.Disconnect()
		}
	})
}