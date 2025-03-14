package main

import (
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

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
    Run() error
}

// Message matches the server's message structure
type Message struct {
    Type      string `json:"type"`
    Token     string `json:"token,omitempty"`
    PeerToken string `json:"peerToken,omitempty"`
    SDP       string `json:"sdp,omitempty"`
    ICE       string `json:"ice,omitempty"`
}

type FileInfo struct {
    Name string `json:"name"`
    Size int64  `json:"size"`
    Type string `json:"type"`
    MD5  string `json:"md5,omitempty"`
}

type ChunkInfo struct {
    Sequence    int `json:"sequence"`
    TotalChunks int `json:"totalChunks"`
    Size        int `json:"size"`
}

type FileTransfer struct {
    *FileInfo
    file       *os.File
    filePath   string
    currentChunk struct {
        sequence int
        total    int
        size     int
    }
}

const (
    defaultChunkSize = 16384 // Default to 16KB for compatibility
    maxSupportedChunkSize = 65536 - 8 // Maximum supported chunk size (64KB - 8 bytes for framing)

    // WebRTC has a limit on message size, and we need to account for framing overhead
    // We add 8 bytes of framing (4 bytes sequence + 4 bytes length)
    maxWebRTCMessageSize = 65536 // 64KB is a safe limit for most WebRTC implementations

    // Use a fixed chunk size for all calculations to ensure consistency
    fixedChunkSize = 65536 - 8 // 64KB - 8 bytes for framing
)

// This will be negotiated during connection
var maxChunkSize = defaultChunkSize

type transferState struct {
    inProgress          bool
    receivedSize        int64
    lastUpdateSize      int64
    fileTransfer        *FileTransfer
    startTime           time.Time
    lastUpdate          time.Time
    chunks              [][]byte
    totalChunks         int
    confirmHandler      func(int) // For handling chunk confirmations

    // Sliding window parameters
    windowSize          int       // Number of chunks to send before waiting for acks
    nextSequenceToSend  int       // Next sequence number to send
    lastAckedSequence   int       // Last sequence number that was acknowledged
    unacknowledgedChunks map[int]bool // Map of sequence numbers to chunks that haven't been acked
    retransmissionQueue []int     // Queue of chunks to retransmit
    retransmissionTimer *time.Timer // Timer for retransmissions
    chunkTimestamps     map[int]time.Time // Map of sequence numbers to timestamps when they were sent
    congestionWindow    int       // Dynamic window size that adjusts based on network conditions
    consecutiveTimeouts int       // Track consecutive timeouts for congestion control
    missingChunks       map[int]bool // Track missing chunks on receive side
    receivedChunks      map[int]bool // Track received chunks
    lastReceivedSequence int      // Last in-order sequence received
    expectedChunk       *ChunkInfo // Expected chunk info for binary data
}

type WebRTCState struct {
    peerToken       string
    isInitiator     bool
    connected       bool
    peerConn        *webrtc.PeerConnection
    controlChannel  *webrtc.DataChannel // For metadata and control messages
    dataChannel     *webrtc.DataChannel // For binary data transfer
    sendTransfer    transferState
    receiveTransfer transferState
}

type Client struct {
    conn    *websocket.Conn
    token   string
    ui      UserInterface
    webrtc  *WebRTCState
}
