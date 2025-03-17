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
	hasOffer   bool
	hasAnswer  bool
}

// NewSignaling creates a new signaling instance
func NewSignaling(connection *Connection, sender MessageSender, logger Logger) *Signaling {
	return &Signaling{
		connection: connection,
		sender:     sender,
		logger:     logger,
		hasOffer:   false,
		hasAnswer:  false,
	}
}

// HandleOffer handles an SDP offer
func (s *Signaling) HandleOffer(msg SignalingMessage) error {
	s.logger.LogDebug("Handling SDP offer")
	
	// Check if we already have an offer
	if s.hasOffer {
		s.logger.LogDebug("Already have an offer, ignoring")
		return fmt.Errorf("already have an offer")
	}
	
	// Parse the SDP offer
	var offer webrtc.SessionDescription
	err := json.Unmarshal([]byte(msg.SDP), &offer)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to parse SDP offer: %v", err))
		return fmt.Errorf("failed to parse SDP offer: %v", err)
	}
	
	s.logger.LogDebug("SDP offer parsed successfully")

	// Set the remote description
	s.logger.LogDebug("Setting remote description")
	err = s.connection.state.PeerConn.SetRemoteDescription(offer)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to set remote description: %v", err))
		return fmt.Errorf("failed to set remote description: %v", err)
	}
	s.logger.LogDebug("Remote description set successfully")
	
	// Mark that we have an offer
	s.hasOffer = true

	// Create an answer
	s.logger.LogDebug("Creating answer")
	answer, err := s.connection.state.PeerConn.CreateAnswer(nil)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to create answer: %v", err))
		return fmt.Errorf("failed to create answer: %v", err)
	}
	s.logger.LogDebug("Answer created successfully")

	// Set the local description
	s.logger.LogDebug("Setting local description")
	err = s.connection.state.PeerConn.SetLocalDescription(answer)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to set local description: %v", err))
		return fmt.Errorf("failed to set local description: %v", err)
	}
	s.logger.LogDebug("Local description set successfully")
	
	// Mark that we have an answer
	s.hasAnswer = true

	// Marshal the answer
	s.logger.LogDebug("Marshaling answer")
	answerObj := struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}{
		Type: answer.Type.String(),
		SDP:  answer.SDP,
	}
	answerJSON, err := json.Marshal(answerObj)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to marshal answer: %v", err))
		return fmt.Errorf("failed to marshal answer: %v", err)
	}
	s.logger.LogDebug("Answer marshaled successfully")

	// Send the answer
	s.logger.LogDebug("Sending answer")
	err = s.sender.SendSignalingMessage(SignalingMessage{
		Type:      "answer",
		PeerToken: msg.Token,
		SDP:       string(answerJSON),
	})
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to send answer: %v", err))
		return fmt.Errorf("failed to send answer: %v", err)
	}
	s.logger.LogDebug("Answer sent successfully")

	return nil
}

// HandleAnswer handles an SDP answer
func (s *Signaling) HandleAnswer(msg SignalingMessage) error {
	s.logger.LogDebug("Handling SDP answer")
	
	// Check if we have an offer
	if !s.hasOffer {
		s.logger.LogDebug("No offer to answer, ignoring")
		return fmt.Errorf("no offer to answer")
	}
	
	// Check if we already have an answer
	if s.hasAnswer {
		s.logger.LogDebug("Already have an answer, ignoring")
		return fmt.Errorf("already have an answer")
	}
	
	// Parse the SDP answer
	var answer webrtc.SessionDescription
	err := json.Unmarshal([]byte(msg.SDP), &answer)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to parse SDP answer: %v", err))
		return fmt.Errorf("failed to parse SDP answer: %v", err)
	}
	s.logger.LogDebug("SDP answer parsed successfully")

	// Set the remote description
	s.logger.LogDebug("Setting remote description")
	err = s.connection.state.PeerConn.SetRemoteDescription(answer)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to set remote description: %v", err))
		return fmt.Errorf("failed to set remote description: %v", err)
	}
	s.logger.LogDebug("Remote description set successfully")
	
	// Mark that we have an answer
	s.hasAnswer = true

	return nil
}

// HandleICE handles an ICE candidate
func (s *Signaling) HandleICE(msg SignalingMessage) error {
	s.logger.LogDebug("Handling ICE candidate")
	
	// Parse the ICE candidate
	var candidate webrtc.ICECandidateInit
	err := json.Unmarshal([]byte(msg.ICE), &candidate)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to parse ICE candidate: %v", err))
		return fmt.Errorf("failed to parse ICE candidate: %v", err)
	}
	s.logger.LogDebug("ICE candidate parsed successfully")

	// Add the ICE candidate
	s.logger.LogDebug("Adding ICE candidate")
	err = s.connection.state.PeerConn.AddICECandidate(candidate)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to add ICE candidate: %v", err))
		return fmt.Errorf("failed to add ICE candidate: %v", err)
	}
	s.logger.LogDebug("ICE candidate added successfully")

	return nil
}

// CreateOffer creates an SDP offer
func (s *Signaling) CreateOffer() error {
	s.logger.LogDebug("Creating SDP offer")
	
	// Check if we already have an offer
	if s.hasOffer {
		s.logger.LogDebug("Already have an offer, ignoring")
		return fmt.Errorf("already have an offer")
	}
	
	// Create an offer
	s.logger.LogDebug("Creating offer")
	offer, err := s.connection.state.PeerConn.CreateOffer(nil)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to create offer: %v", err))
		return fmt.Errorf("failed to create offer: %v", err)
	}
	s.logger.LogDebug("Offer created successfully")

	// Set the local description
	s.logger.LogDebug("Setting local description")
	err = s.connection.state.PeerConn.SetLocalDescription(offer)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to set local description: %v", err))
		return fmt.Errorf("failed to set local description: %v", err)
	}
	s.logger.LogDebug("Local description set successfully")
	
	// Mark that we have an offer
	s.hasOffer = true

	// Marshal the offer
	s.logger.LogDebug("Marshaling offer")
	offerObj := struct {
		Type string `json:"type"`
		SDP  string `json:"sdp"`
	}{
		Type: offer.Type.String(),
		SDP:  offer.SDP,
	}
	offerJSON, err := json.Marshal(offerObj)
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to marshal offer: %v", err))
		return fmt.Errorf("failed to marshal offer: %v", err)
	}
	s.logger.LogDebug("Offer marshaled successfully")

	// Send the offer
	s.logger.LogDebug("Sending offer")
	err = s.sender.SendSignalingMessage(SignalingMessage{
		Type:      "offer",
		PeerToken: s.connection.state.PeerToken,
		SDP:       string(offerJSON),
	})
	if err != nil {
		s.logger.LogDebug(fmt.Sprintf("Failed to send offer: %v", err))
		return fmt.Errorf("failed to send offer: %v", err)
	}
	s.logger.LogDebug("Offer sent successfully")

	return nil
}

// SetupICEHandlers sets up handlers for ICE candidates
func (s *Signaling) SetupICEHandlers() {
	// Set up ICE candidate handler
	s.connection.state.PeerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			return
		}

		s.logger.LogDebug(fmt.Sprintf("ICE candidate: %s", candidate.String()))

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
}

// Reset resets the signaling state
func (s *Signaling) Reset() {
	s.hasOffer = false
	s.hasAnswer = false
}