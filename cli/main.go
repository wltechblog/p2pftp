package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

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
    file     *os.File
    filePath string
    currentChunk struct {
        sequence int
        total   int
        size    int
    }
}

const (
    maxChunkSize = 16384 // 16KB chunks for WebRTC compatibility
)

type WebRTCState struct {
    peerToken    string
    isInitiator  bool
    connected    bool
    peerConn     *webrtc.PeerConnection
    dataChannel  *webrtc.DataChannel
    receivedSize int64
    fileTransfer *FileTransfer
    startTime    time.Time
    chunks       [][]byte
    totalChunks  int
}

type Client struct {
    conn   *websocket.Conn
    token  string
    ui     *UI
    webrtc *WebRTCState
}

func main() {
    addr := flag.String("addr", "localhost:8089", "server address")
    flag.Parse()

    u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
    log.Printf("Connecting to %s...", u.String())

    conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        log.Fatal("WebSocket dial error:", err)
    }
    defer conn.Close()

    client := &Client{
        conn:   conn,
        webrtc: &WebRTCState{},
    }

    ui := NewUI(client)
    client.ui = ui

    go client.handleMessages()

    if err := ui.Run(); err != nil {
        fmt.Printf("Error running UI: %v\n", err)
    }
}

func calculateMD5(filePath string) (string, error) {
    file, err := os.Open(filePath)
    if err != nil {
        return "", fmt.Errorf("failed to open file: %v", err)
    }
    defer file.Close()

    hash := md5.New()
    buf := make([]byte, 32768)

    for {
        n, err := file.Read(buf)
        if n > 0 {
            hash.Write(buf[:n])
        }
        if err == io.EOF {
            break
        }
        if err != nil {
            return "", err
        }
    }

    return hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *Client) SendMessage(msg Message) error {
    err := c.conn.WriteJSON(msg)
    if err != nil {
        c.ui.ShowError("Send failed: " + err.Error())
        return err
    }
    return nil
}

func (c *Client) Connect(peerToken string) error {
    if c.webrtc.connected {
        return fmt.Errorf("already connected to a peer")
    }
    c.webrtc = &WebRTCState{
        peerToken:   peerToken,
        isInitiator: true,
    }
    return c.SendMessage(Message{Type: "connect", PeerToken: peerToken})
}

func (c *Client) Accept(peerToken string) error {
    if c.webrtc.connected {
        return fmt.Errorf("already connected to a peer")
    }
    c.webrtc = &WebRTCState{peerToken: peerToken, isInitiator: false}
    return c.SendMessage(Message{Type: "accept", PeerToken: peerToken})
}

func (c *Client) Reject(peerToken string) error {
    if c.webrtc.connected {
        c.disconnectPeer()
    }
    return c.SendMessage(Message{Type: "reject", PeerToken: peerToken})
}

func (c *Client) disconnectPeer() {
    if c.webrtc.fileTransfer != nil && c.webrtc.fileTransfer.file != nil {
        c.webrtc.fileTransfer.file.Close()
    }
    if c.webrtc.peerConn != nil {
        c.webrtc.peerConn.Close()
        c.webrtc.peerConn = nil
    }
    if c.webrtc.dataChannel != nil {
        c.webrtc.dataChannel.Close()
        c.webrtc.dataChannel = nil
    }
    c.webrtc = &WebRTCState{}
}

func (c *Client) setupPeerConnection() error {
    config := webrtc.Configuration{
        ICEServers: []webrtc.ICEServer{
            {
                URLs: []string{
                    "stun:stun.l.google.com:19302",
                    "stun:stun1.l.google.com:19302",
                },
            },
        },
    }

    peerConn, err := webrtc.NewPeerConnection(config)
    if err != nil {
        return fmt.Errorf("failed to create peer connection: %v", err)
    }

    // Monitor connection state changes
    peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
        c.ui.LogDebug(fmt.Sprintf("Connection state changed to: %s", state))
        switch state {
        case webrtc.PeerConnectionStateFailed:
            c.ui.ShowError("Connection failed - attempting ICE restart")
            // Try ICE restart
            if offer, err := peerConn.CreateOffer(&webrtc.OfferOptions{ICERestart: true}); err == nil {
                if err := peerConn.SetLocalDescription(offer); err == nil {
                    c.SendMessage(Message{
                        Type:      "offer",
                        PeerToken: c.webrtc.peerToken,
                        SDP:       offer.SDP,
                    })
                }
            }
        case webrtc.PeerConnectionStateDisconnected:
            c.ui.LogDebug("Connection disconnected - waiting for reconnection")
        case webrtc.PeerConnectionStateClosed:
            c.ui.LogDebug("Connection closed")
            c.disconnectPeer()
        }
    })

    peerConn.OnSignalingStateChange(func(state webrtc.SignalingState) {
        c.ui.LogDebug(fmt.Sprintf("Signaling state changed to: %s", state))
    })

    peerConn.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
        c.ui.LogDebug(fmt.Sprintf("ICE gathering state changed to: %s", state))
    })

    ordered := true
    maxRetransmits := uint16(30)
    negotiated := true
    id := uint16(1)
    dataChannelConfig := &webrtc.DataChannelInit{
        Ordered:        &ordered,
        MaxRetransmits: &maxRetransmits,
        Negotiated:     &negotiated,
        ID:            &id,
    }

    dataChannel, err := peerConn.CreateDataChannel("p2pftp", dataChannelConfig)
    if err != nil {
        return fmt.Errorf("failed to create data channel: %v", err)
    }

    c.setupDataChannel(dataChannel)
    c.webrtc.peerConn = peerConn

    // Add ICE candidate handler
    peerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
        if candidate == nil {
            return
        }

        candidateJSON, err := json.Marshal(candidate.ToJSON())
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to marshal ICE candidate: %v", err))
            return
        }

        err = c.SendMessage(Message{
            Type:      "ice",
            PeerToken: c.webrtc.peerToken,
            ICE:       string(candidateJSON),
        })
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to send ICE candidate: %v", err))
        }
    })

    return nil
}

func (c *Client) setupDataChannel(channel *webrtc.DataChannel) {
    c.webrtc.dataChannel = channel

    channel.OnOpen(func() {
        c.webrtc.connected = true
        c.ui.LogDebug("Data channel ready for transfer")
        c.ui.ShowChat(c.webrtc.peerToken, "Connected to peer")
        c.ui.ShowFileTransfer("Ready for file transfer")
    })

    channel.OnClose(func() {
        c.webrtc.connected = false
        if c.webrtc.fileTransfer != nil && c.webrtc.fileTransfer.file != nil {
            c.webrtc.fileTransfer.file.Close()
            c.webrtc.fileTransfer = nil
        }
    })

    channel.OnMessage(func(msg webrtc.DataChannelMessage) {
        if msg.IsString {
            var data map[string]interface{}
            if err := json.Unmarshal(msg.Data, &data); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse message: %v", err))
                return
            }
            // Handle message based on type
            if msgType, ok := data["type"].(string); ok {
                switch msgType {
                case "message":
                    if content, ok := data["content"].(string); ok {
                        c.ui.ShowChat(c.webrtc.peerToken, content)
                    }
                case "file-info":
                    // Handle file info
                    c.handleFileInfo(data)
                case "chunk":
                    // Store chunk metadata for next binary message
                    if seq, ok := data["sequence"].(float64); ok {
                        c.webrtc.fileTransfer.currentChunk.sequence = int(seq)
                    }
                    if total, ok := data["total"].(float64); ok {
                        c.webrtc.fileTransfer.currentChunk.total = int(total)
                    }
                    if size, ok := data["size"].(float64); ok {
                        c.webrtc.fileTransfer.currentChunk.size = int(size)
                    }
                case "file-complete":
                    c.handleFileComplete()
                }
            }
        } else {
            c.handleBinaryData(msg.Data)
        }
    })
}

func (c *Client) handleFileInfo(info map[string]interface{}) {
    // Create downloads directory if it doesn't exist
    downloadDir := "downloads"
    os.MkdirAll(downloadDir, 0755)

    fileInfo, ok := info["info"].(map[string]interface{})
    if !ok {
        c.ui.ShowError("Invalid file info format")
        return
    }

    name, _ := fileInfo["name"].(string)
    size, _ := fileInfo["size"].(float64)
    md5, _ := fileInfo["md5"].(string)

    filePath := filepath.Join(downloadDir, name)
    file, err := os.Create(filePath)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to create file: %v", err))
        return
    }

    totalChunks := int(math.Ceil(float64(size) / float64(maxChunkSize)))
    c.webrtc.fileTransfer = &FileTransfer{
        FileInfo: &FileInfo{
            Name: name,
            Size: int64(size),
            MD5:  md5,
        },
        file:     file,
        filePath: filePath,
    }
    c.webrtc.receivedSize = 0
    c.webrtc.chunks = make([][]byte, totalChunks)
    c.ui.ShowFileTransfer(fmt.Sprintf("Receiving %s (0/%d bytes) - 0%%", name, int64(size)))
}

func (c *Client) handleBinaryData(data []byte) {
    if c.webrtc.fileTransfer == nil || c.webrtc.fileTransfer.file == nil {
        return
    }

    // Verify chunk size matches metadata
    if len(data) != c.webrtc.fileTransfer.currentChunk.size {
        c.ui.ShowError(fmt.Sprintf("Chunk size mismatch. Expected: %d, Got: %d",
            c.webrtc.fileTransfer.currentChunk.size, len(data)))
        return
    }

    sequence := c.webrtc.fileTransfer.currentChunk.sequence
    total := c.webrtc.fileTransfer.currentChunk.total

    // Store chunk at correct position
    c.webrtc.chunks[sequence] = make([]byte, len(data))
    copy(c.webrtc.chunks[sequence], data)
    c.webrtc.receivedSize += int64(len(data))

    percentage := int((float64(c.webrtc.receivedSize) / float64(c.webrtc.fileTransfer.Size)) * 100)
    c.ui.ShowFileTransfer(fmt.Sprintf("Receiving %s - Chunk %d/%d (%d/%d bytes) - %d%%",
        c.webrtc.fileTransfer.Name,
        sequence + 1,
        total,
        c.webrtc.receivedSize,
        c.webrtc.fileTransfer.Size,
        percentage))
}

func (c *Client) handleFileComplete() {
    if c.webrtc.fileTransfer == nil || c.webrtc.fileTransfer.file == nil {
        return
    }

    // Write all chunks to file
    for i, chunk := range c.webrtc.chunks {
        if chunk == nil {
            c.ui.ShowError(fmt.Sprintf("Missing chunk %d/%d", i+1, len(c.webrtc.chunks)))
            c.webrtc.fileTransfer.file.Close()
            return
        }

        if _, err := c.webrtc.fileTransfer.file.Write(chunk); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to write chunk: %v", err))
            c.webrtc.fileTransfer.file.Close()
            return
        }
    }

    // Close file before calculating MD5
    c.webrtc.fileTransfer.file.Close()

    // Validate MD5 checksum if provided
    if c.webrtc.fileTransfer.MD5 != "" {
        c.ui.ShowFileTransfer("Validating file integrity...")
        receivedMD5, err := calculateMD5(c.webrtc.fileTransfer.filePath)
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to calculate MD5: %v", err))
        } else if receivedMD5 != c.webrtc.fileTransfer.MD5 {
            c.ui.ShowError("⚠️ File integrity check failed! The file may be corrupted.")
            c.ui.ShowFileTransfer(fmt.Sprintf("MD5 mismatch - Expected: %s, Got: %s", 
                c.webrtc.fileTransfer.MD5, receivedMD5))
        } else {
            c.ui.ShowFileTransfer(fmt.Sprintf("✓ File integrity verified (MD5: %s)", receivedMD5))
        }
    }

    c.ui.ShowFileTransfer(fmt.Sprintf("Saved file to: %s", c.webrtc.fileTransfer.filePath))
    c.webrtc.fileTransfer = nil
    c.webrtc.chunks = nil
}

func (c *Client) SendChat(text string) error {
    if !c.webrtc.connected {
        return fmt.Errorf("not connected to peer")
    }

    msg := struct {
        Type    string `json:"type"`
        Content string `json:"content"`
    }{
        Type:    "message",
        Content: text,
    }

    msgJSON, err := json.Marshal(msg)
    if err != nil {
        return fmt.Errorf("failed to marshal message: %v", err)
    }

    err = c.webrtc.dataChannel.SendText(string(msgJSON))
    if err != nil {
        return fmt.Errorf("failed to send message: %v", err)
    }

    return nil
}

func (c *Client) SendFile(filePath string) error {
    if !c.webrtc.connected {
        return fmt.Errorf("not connected to peer")
    }

    file, err := os.Open(filePath)
    if err != nil {
        return fmt.Errorf("failed to open file: %v", err)
    }
    defer file.Close()

    info, err := file.Stat()
    if err != nil {
        return fmt.Errorf("failed to get file info: %v", err)
    }

    md5Hash, err := calculateMD5(filePath)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to calculate MD5: %v", err))
        md5Hash = ""
    }

    // Send file info
    infoMsg := struct {
        Type string `json:"type"`
        Info struct {
            Name string `json:"name"`
            Size int64  `json:"size"`
            MD5  string `json:"md5"`
        } `json:"info"`
    }{
        Type: "file-info",
    }
    infoMsg.Info.Name = info.Name()
    infoMsg.Info.Size = info.Size()
    infoMsg.Info.MD5 = md5Hash

    infoJSON, err := json.Marshal(infoMsg)
    if err != nil {
        return fmt.Errorf("failed to marshal file info: %v", err)
    }

    err = c.webrtc.dataChannel.SendText(string(infoJSON))
    if err != nil {
        return fmt.Errorf("failed to send file info: %v", err)
    }

    // Send file in chunks with flow control
    buffer := make([]byte, maxChunkSize)
    totalSent := int64(0)
    startTime := time.Now()
    totalChunks := int(math.Ceil(float64(info.Size()) / float64(maxChunkSize)))
    chunkIndex := 0

    for {
        n, err := file.Read(buffer)
        if err == io.EOF {
            break
        }
        if err != nil {
            return fmt.Errorf("failed to read file: %v", err)
        }

        // Wait if buffer is getting too full
        for c.webrtc.dataChannel.BufferedAmount() > maxChunkSize*16 {
            time.Sleep(100 * time.Millisecond)
        }

        // Send chunk metadata with actual size
        chunkInfo := struct {
            Type     string `json:"type"`
            Sequence int    `json:"sequence"`
            Total    int    `json:"total"`
            Size     int    `json:"size"`
        }{
            Type:     "chunk",
            Sequence: chunkIndex,
            Total:    totalChunks,
            Size:     n,
        }

        chunkInfoJSON, err := json.Marshal(chunkInfo)
        if err != nil {
            return fmt.Errorf("failed to marshal chunk info: %v", err)
        }

        err = c.webrtc.dataChannel.SendText(string(chunkInfoJSON))
        if err != nil {
            return fmt.Errorf("failed to send chunk info: %v", err)
        }

        // Send binary chunk (no need to pad to maxChunkSize)
        err = c.webrtc.dataChannel.Send(buffer[:n])
        if err != nil {
            return fmt.Errorf("failed to send chunk: %v", err)
        }

        totalSent += int64(n)
        chunkIndex++

        if time.Since(startTime) > 200*time.Millisecond {
            percentage := int((float64(totalSent) / float64(info.Size())) * 100)
            rate := float64(totalSent) / time.Since(startTime).Seconds() / 1024 // KB/s
            c.ui.ShowFileTransfer(fmt.Sprintf("Sending %s - Chunk %d/%d (%d/%d bytes) - %d%% (%.1f KB/s)",
                info.Name(), chunkIndex, totalChunks, totalSent, info.Size(), percentage, rate))
            startTime = time.Now()
        }
    }

    // Send complete message
    completeMsg := struct {
        Type string `json:"type"`
    }{
        Type: "file-complete",
    }

    completeJSON, err := json.Marshal(completeMsg)
    if err != nil {
        return fmt.Errorf("failed to marshal complete message: %v", err)
    }

    err = c.webrtc.dataChannel.SendText(string(completeJSON))
    if err != nil {
        return fmt.Errorf("failed to send complete message: %v", err)
    }

    return nil
}

func (c *Client) handleMessages() {
    c.ui.LogDebug("Message handler started, waiting for server token...")
    for {
        var msg Message
        err := c.conn.ReadJSON(&msg)
        if err != nil {
            c.ui.LogDebug(fmt.Sprintf("Error reading message: %v", err))
            c.ui.ShowError("Connection error - please try again")
            return
        }

        switch msg.Type {
        case "token":
            c.token = msg.Token
            c.ui.SetToken(msg.Token)

        case "request":
            c.ui.ShowConnectionRequest(msg.Token)
            c.webrtc.peerToken = msg.Token
            c.webrtc.isInitiator = false

        case "accepted":
            if c.webrtc.peerToken == "" {
                c.ui.ShowError("No active connection attempt")
                continue
            }

            if err := c.setupPeerConnection(); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
                continue
            }

            offer, err := c.webrtc.peerConn.CreateOffer(nil)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to create offer: %v", err))
                continue
            }

            err = c.webrtc.peerConn.SetLocalDescription(offer)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
                continue
            }

            // Send double wrapped SDP
            sdpObj := struct {
                Type string `json:"type"`
                SDP  string `json:"sdp"`
            }{
                Type: "offer",
                SDP:  offer.SDP,
            }

            sdpJSON, err := json.Marshal(sdpObj)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to marshal SDP object: %v", err))
                continue
            }

            err = c.SendMessage(Message{
                Type:      "offer",
                PeerToken: c.webrtc.peerToken,
                SDP:       string(sdpJSON),
            })
            if err != nil {
                c.ui.ShowError("Failed to send offer")
                continue
            }

        case "offer":
            if err := c.setupPeerConnection(); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
                continue
            }

            // Parse the double-wrapped SDP
            var sdpObj struct {
                Type string `json:"type"`
                SDP  string `json:"sdp"`
            }
            if err := json.Unmarshal([]byte(msg.SDP), &sdpObj); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse SDP object: %v", err))
                c.ui.LogDebug(fmt.Sprintf("Raw SDP: %s", msg.SDP))
                continue
            }

            offer := webrtc.SessionDescription{
                Type: webrtc.SDPTypeOffer,
                SDP:  sdpObj.SDP,
            }

            c.ui.LogDebug(fmt.Sprintf("Received offer SDP: %s", sdpObj.SDP))
            err = c.webrtc.peerConn.SetRemoteDescription(offer)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
                continue
            }

            answer, err := c.webrtc.peerConn.CreateAnswer(nil)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to create answer: %v", err))
                continue
            }

            err = c.webrtc.peerConn.SetLocalDescription(answer)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
                continue
            }

            // Convert answer to JSON
            answerMsg := struct {
                Type string `json:"type"`
                SDP  string `json:"sdp"`
            }{
                Type: "answer",
                SDP:  answer.SDP,
            }

            answerJSON, err := json.Marshal(answerMsg)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to marshal answer SDP: %v", err))
                continue
            }

            err = c.SendMessage(Message{
                Type:      "answer",
                PeerToken: c.webrtc.peerToken,
                SDP:       string(answerJSON),
            })
            if err != nil {
                c.ui.ShowError("Failed to send answer")
                continue
            }

        case "answer":
            // Parse the double-wrapped SDP
            var sdpObj struct {
                Type string `json:"type"`
                SDP  string `json:"sdp"`
            }
            if err := json.Unmarshal([]byte(msg.SDP), &sdpObj); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse SDP object: %v", err))
                c.ui.LogDebug(fmt.Sprintf("Raw SDP: %s", msg.SDP))
                continue
            }

            answer := webrtc.SessionDescription{
                Type: webrtc.SDPTypeAnswer,
                SDP:  sdpObj.SDP,
            }

            c.ui.LogDebug(fmt.Sprintf("Received answer SDP: %s", sdpObj.SDP))
            err = c.webrtc.peerConn.SetRemoteDescription(answer)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
                continue
            }

        case "ice":
            var candidate webrtc.ICECandidateInit
            if err := json.Unmarshal([]byte(msg.ICE), &candidate); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse ICE candidate: %v", err))
                continue
            }

            err = c.webrtc.peerConn.AddICECandidate(candidate)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to add ICE candidate: %v", err))
                continue
            }

        case "rejected":
            c.ui.ShowConnectionRejected(msg.Token)
            c.disconnectPeer()

        case "error":
            c.ui.ShowError(msg.SDP)
            c.disconnectPeer()
        }
    }
}
