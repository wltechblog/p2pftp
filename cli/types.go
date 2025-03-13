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
    maxChunkSize = 16384 // 16KB chunks for WebRTC compatibility
)

type transferState struct {
    inProgress    bool
    receivedSize  int64
    fileTransfer  *FileTransfer
    startTime     time.Time
    chunks        [][]byte
    totalChunks   int
}

type WebRTCState struct {
    peerToken      string
    isInitiator    bool
    connected      bool
    peerConn       *webrtc.PeerConnection
    dataChannel    *webrtc.DataChannel
    sendTransfer   transferState
    receiveTransfer transferState
}

type Client struct {
    conn    *websocket.Conn
    token   string
    ui      UserInterface
    webrtc  *WebRTCState
}
