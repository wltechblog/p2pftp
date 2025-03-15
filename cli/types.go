package main

// This file contains common types and interfaces used throughout the application.
// It helps avoid circular dependencies between packages.

// UserInterface defines the interface for the UI implementation
type UserInterface interface {
	ShowError(msg string)
	LogDebug(msg string)
	ShowChat(from string, msg string)
	ShowConnectionRequest(token string)
	ShowConnectionAccepted(msg string)
	ShowConnectionRejected(token string)
	SetToken(token string)
	UpdateTransferProgress(status string, direction string)
}

// MessageSender sends messages to the signaling server
type MessageSender interface {
	SendMessage(msg Message) error
}

// Message represents a message exchanged with the server
type Message struct {
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	PeerToken string `json:"peerToken,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	ICE       string `json:"ice,omitempty"`
}

// FileInfo contains metadata about a file being transferred
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

// FileTransfer contains information about a file transfer
type FileTransfer struct {
	*FileInfo
	file     interface{}
	filePath string
}

// TransferState contains the state of a file transfer
type TransferState struct {
	inProgress          bool
	startTime           interface{}
	lastUpdate          interface{}
	fileTransfer        *FileTransfer
	chunks              [][]byte
	totalChunks         int
	lastReceivedSequence int
	receivedChunks      map[int]bool
	missingChunks       map[int]bool
	receivedSize        int64
	lastUpdateSize      int64
	windowSize          int
	nextSequenceToSend  int
	lastAckedSequence   int
	unacknowledgedChunks map[int]bool
	retransmissionQueue []int
	chunkTimestamps     map[int]interface{}
	congestionWindow    int
	consecutiveTimeouts int
	confirmHandler      func(int)
}