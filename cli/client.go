package main

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
)

func NewClient(conn *websocket.Conn) *Client {
    return &Client{
        conn:   conn,
        webrtc: &WebRTCState{},
    }
}

// ChunkConfirm message for acknowledging received chunks
type ChunkConfirm struct {
    Type     string `json:"type"`
    Sequence int    `json:"sequence"`
}

func (c *Client) handleChunkConfirm(sequence int) {
    if c.webrtc.sendTransfer.confirmHandler != nil {
        c.webrtc.sendTransfer.confirmHandler(sequence)
    }
}

func (c *Client) SendMessage(msg Message) error {
    err := c.conn.WriteJSON(msg)
    if err != nil {
        c.ui.ShowError("Send failed: " + err.Error())
        return err
    }
    return nil
}

func (c *Client) SendFile(path string) error {
    if !c.webrtc.connected {
        return fmt.Errorf("not connected to peer")
    }

    if c.webrtc.sendTransfer.inProgress {
        return fmt.Errorf("upload already in progress")
    }

    file, err := os.Open(path)
    if err != nil {
        return fmt.Errorf("failed to open file: %v", err)
    }
    defer file.Close()

    info, err := file.Stat()
    if err != nil {
        return fmt.Errorf("failed to get file info: %v", err)
    }

    // Calculate file hash
    fileHash, err := calculateMD5(path)
    if err != nil {
        return fmt.Errorf("failed to calculate file hash: %v", err)
    }

    c.webrtc.sendTransfer = transferState{
        inProgress: true,
        startTime:  time.Now(),
        fileTransfer: &FileTransfer{
            FileInfo: &FileInfo{
                Name: info.Name(),
                Size: info.Size(),
                MD5:  fileHash,
            },
        },
    }

    fileInfo := struct {
        Type string   `json:"type"`
        Info FileInfo `json:"info"`
    }{
        Type: "file-info",
        Info: FileInfo{
            Name: info.Name(),
            Size: info.Size(),
            MD5:  fileHash,
        },
    }

    infoJSON, err := json.Marshal(fileInfo)
    if err != nil {
        return fmt.Errorf("failed to marshal file info: %v", err)
    }

    err = c.webrtc.dataChannel.SendText(string(infoJSON))
    if err != nil {
        return fmt.Errorf("failed to send file info: %v", err)
    }

    // Show initial status
    c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s [0%%] (0/s)", info.Name()), "send")

    // Send file in chunks
    buf := make([]byte, maxChunkSize)
    totalChunks := int(math.Ceil(float64(info.Size()) / float64(maxChunkSize)))
    sentChunks := 0
    totalSent := int64(0)
    lastUpdate := time.Now()
    lastUpdateSize := int64(0)
    
    // Setup confirmation channel for each chunk
    chunkConfirms := make(chan int, totalChunks)
    
    // Setup confirmation handler
    c.webrtc.sendTransfer.confirmHandler = func(sequence int) {
        chunkConfirms <- sequence
    }

    // Start reading and sending chunks
    for {
        n, err := file.Read(buf)
        if err == io.EOF {
            break
        }
        if err != nil {
            return fmt.Errorf("failed to read file: %v", err)
        }

        chunk := struct {
            Type     string `json:"type"`
            Sequence int    `json:"sequence"`
            Total    int    `json:"total"`
            Size     int    `json:"size"`
            Data     string `json:"data"`
        }{
            Type:     "chunk",
            Sequence: sentChunks,
            Total:    totalChunks,
            Size:     n,
            Data:     base64.StdEncoding.EncodeToString(buf[:n]),
        }

        chunkJSON, err := json.Marshal(chunk)
        if err != nil {
            return fmt.Errorf("failed to marshal chunk: %v", err)
        }

        // Send chunk
        err = c.webrtc.dataChannel.SendText(string(chunkJSON))
        if err != nil {
            return fmt.Errorf("failed to send chunk: %v", err)
        }

        // Wait for chunk confirmation
        select {
        case <-chunkConfirms:
            totalSent += int64(n)
            sentChunks++

            // Update progress
            now := time.Now()
            if time.Since(lastUpdate) > 100*time.Millisecond {
                timeDiff := now.Sub(lastUpdate).Seconds()
                if timeDiff > 0 {
                    speed := float64(totalSent-lastUpdateSize) / timeDiff
                    percentage := int((float64(totalSent) / float64(info.Size())) * 100)
                    c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s [%d%%] (%.1f MB/s)",
                        info.Name(),
                        percentage,
                        speed/1024/1024),
                        "send")
                    lastUpdate = now
                    lastUpdateSize = totalSent
                }
            }
        case <-time.After(5 * time.Second):
            return fmt.Errorf("timeout waiting for chunk confirmation")
        }
    }

    // Calculate final statistics
    avgSpeed := float64(info.Size()) / time.Since(c.webrtc.sendTransfer.startTime).Seconds()

    // Show completion message
    c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s - Finishing transfer...", info.Name()), "send")

    // Send complete message
    complete := struct {
        Type string `json:"type"`
    }{
        Type: "file-complete",
    }

    completeJSON, err := json.Marshal(complete)
    if err != nil {
        return fmt.Errorf("failed to marshal complete message: %v", err)
    }

    err = c.webrtc.dataChannel.SendText(string(completeJSON))
    if err != nil {
        return fmt.Errorf("failed to send complete message: %v", err)
    }

    // Wait a moment for the message to be sent
    time.Sleep(100 * time.Millisecond)

    // Show final completion message
    c.ui.UpdateTransferProgress(fmt.Sprintf("⬆ %s - Complete (avg: %.1f MB/s)",
        info.Name(),
        avgSpeed/1024/1024),
        "send")

    // Reset transfer state
    c.webrtc.sendTransfer = transferState{}

    return nil
}

func (c *Client) handleChunkData(sequence int, total int, size int, data string) {
    if !c.webrtc.receiveTransfer.inProgress {
        c.ui.ShowError("Received chunk but no download in progress")
        return
    }

    binaryData, err := base64.StdEncoding.DecodeString(data)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to decode chunk data: %v", err))
        return
    }

    if len(binaryData) != size {
        c.ui.ShowError(fmt.Sprintf("Chunk size mismatch. Expected: %d, Got: %d",
            size, len(binaryData)))
        return
    }

    // Store chunk and update size
    c.webrtc.receiveTransfer.chunks[sequence] = binaryData
    
    // Recalculate total received size from scratch to ensure accuracy
    var totalReceived int64
    for _, chunk := range c.webrtc.receiveTransfer.chunks {
        if chunk != nil {
            totalReceived += int64(len(chunk))
        }
    }
    c.webrtc.receiveTransfer.receivedSize = totalReceived

    // Send confirmation back
    confirm := ChunkConfirm{
        Type:     "chunk-confirm",
        Sequence: sequence,
    }
    confirmJSON, err := json.Marshal(confirm)
    if err == nil {
        c.webrtc.dataChannel.SendText(string(confirmJSON))
    }

    // Update progress every 100ms or on significant changes
    now := time.Now()
    received := c.webrtc.receiveTransfer.receivedSize
    totalSize := c.webrtc.receiveTransfer.fileTransfer.Size
    percentage := int((float64(received) / float64(totalSize)) * 100)
    lastPercentage := int((float64(c.webrtc.receiveTransfer.lastUpdateSize) / float64(totalSize)) * 100)

    if time.Since(c.webrtc.receiveTransfer.lastUpdate) > 100*time.Millisecond || 
       percentage != lastPercentage {
        // Calculate speed based on data received since last update
        timeDiff := now.Sub(c.webrtc.receiveTransfer.lastUpdate).Seconds()
        if timeDiff > 0 {
            speed := float64(c.webrtc.receiveTransfer.receivedSize-c.webrtc.receiveTransfer.lastUpdateSize) / timeDiff
            c.ui.UpdateTransferProgress(fmt.Sprintf("⬇ %s [%d%%] (%.1f MB/s)",
                c.webrtc.receiveTransfer.fileTransfer.Name,
                percentage,
                speed/1024/1024),
                "receive")
        }
        c.webrtc.receiveTransfer.lastUpdate = now
        c.webrtc.receiveTransfer.lastUpdateSize = c.webrtc.receiveTransfer.receivedSize
    }

    // Check if file is complete
    if c.webrtc.receiveTransfer.receivedSize >= c.webrtc.receiveTransfer.fileTransfer.Size {
        c.handleFileComplete()
    }
}

func (c *Client) handleFileComplete() {
    if !c.webrtc.receiveTransfer.inProgress {
        return
    }

    // Write all chunks to file
    for i, chunk := range c.webrtc.receiveTransfer.chunks {
        if chunk == nil {
            c.ui.ShowError(fmt.Sprintf("Missing chunk %d", i))
            c.webrtc.receiveTransfer.fileTransfer.file.Close()
            c.webrtc.receiveTransfer = transferState{}
            return
        }

        _, err := c.webrtc.receiveTransfer.fileTransfer.file.Write(chunk)
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to write chunk: %v", err))
            c.webrtc.receiveTransfer.fileTransfer.file.Close()
            c.webrtc.receiveTransfer = transferState{}
            return
        }
    }

    // Close file first
    c.webrtc.receiveTransfer.fileTransfer.file.Close()

    // Compute file hash and verify integrity
    fileHash, err := calculateMD5(c.webrtc.receiveTransfer.fileTransfer.filePath)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to calculate file hash: %v", err))
        c.webrtc.receiveTransfer = transferState{}
        return
    }
    
    // Verify against provided hash if available
    if c.webrtc.receiveTransfer.fileTransfer.MD5 != "" {
        if fileHash != c.webrtc.receiveTransfer.fileTransfer.MD5 {
            c.ui.ShowError(fmt.Sprintf("File integrity check failed:\nExpected MD5: %s\nActual MD5:   %s", 
                c.webrtc.receiveTransfer.fileTransfer.MD5, fileHash))
            c.webrtc.receiveTransfer = transferState{}
            return
        }
        c.ui.LogDebug(fmt.Sprintf("File integrity verified (MD5: %s)", fileHash))
    } else {
        c.ui.LogDebug(fmt.Sprintf("File received (MD5: %s)", fileHash))
    }

    // Calculate transfer statistics
    avgSpeed := float64(c.webrtc.receiveTransfer.fileTransfer.Size) / time.Since(c.webrtc.receiveTransfer.startTime).Seconds()
    c.ui.UpdateTransferProgress(fmt.Sprintf("⬇ %s - Complete (avg: %.1f MB/s)",
        c.webrtc.receiveTransfer.fileTransfer.Name,
        avgSpeed/1024/1024),
        "receive")

    // Reset transfer state
    c.webrtc.receiveTransfer = transferState{}
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
    if c.webrtc.sendTransfer.fileTransfer != nil && c.webrtc.sendTransfer.fileTransfer.file != nil {
        c.webrtc.sendTransfer.fileTransfer.file.Close()
    }
    if c.webrtc.receiveTransfer.fileTransfer != nil && c.webrtc.receiveTransfer.fileTransfer.file != nil {
        c.webrtc.receiveTransfer.fileTransfer.file.Close()
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

            if err := c.webrtc.peerConn.SetLocalDescription(offer); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
                continue
            }

            err = c.SendMessage(Message{
                Type:      "offer",
                PeerToken: c.webrtc.peerToken,
                SDP:       string(offer.SDP),
            })
            if err != nil {
                c.ui.ShowError("Failed to send offer")
                continue
            }

        case "offer", "answer":
            c.handleSDP(msg)

        case "ice":
            var candidate webrtc.ICECandidateInit
            if err := json.Unmarshal([]byte(msg.ICE), &candidate); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse ICE candidate: %v", err))
                continue
            }

            if err := c.webrtc.peerConn.AddICECandidate(candidate); err != nil {
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

    peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
        c.ui.LogDebug(fmt.Sprintf("Connection state changed to: %s", state))
        switch state {
        case webrtc.PeerConnectionStateFailed:
            c.ui.ShowError("Connection failed - attempting ICE restart")
            if offer, err := peerConn.CreateOffer(&webrtc.OfferOptions{ICERestart: true}); err == nil {
                if err := peerConn.SetLocalDescription(offer); err == nil {
                    c.SendMessage(Message{
                        Type:      "offer",
                        PeerToken: c.webrtc.peerToken,
                        SDP:       string(offer.SDP),
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

    dataChannel.OnOpen(func() {
        c.webrtc.connected = true
        c.ui.LogDebug("Data channel ready for transfer")
        c.ui.ShowConnectionAccepted("")
    })

    dataChannel.OnClose(func() {
        c.webrtc.connected = false
        c.disconnectPeer()
    })

    dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
        if !msg.IsString {
            c.ui.ShowError("Unexpected binary message")
            return
        }

        var data map[string]interface{}
        if err := json.Unmarshal(msg.Data, &data); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to parse message: %v", err))
            return
        }

        msgType, ok := data["type"].(string)
        if !ok {
            c.ui.ShowError("Missing message type")
            return
        }

        switch msgType {
        case "message":
            if content, ok := data["content"].(string); ok {
                c.ui.ShowChat(c.webrtc.peerToken, content)
            }
        case "file-info":
            c.handleFileInfo(data)
        case "chunk":
            if sequence, ok := data["sequence"].(float64); ok {
                if total, ok := data["total"].(float64); ok {
                    if size, ok := data["size"].(float64); ok {
                        if base64Data, ok := data["data"].(string); ok {
                            c.handleChunkData(int(sequence), int(total), int(size), base64Data)
                        }
                    }
                }
            }
        case "chunk-confirm":
            if sequence, ok := data["sequence"].(float64); ok {
                c.handleChunkConfirm(int(sequence))
            }
        case "file-complete":
            c.handleFileComplete()
        }
    })

    c.webrtc.dataChannel = dataChannel
    c.webrtc.peerConn = peerConn

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

func (c *Client) handleSDP(msg Message) {
    var sdpObj struct {
        Type string `json:"type"`
        SDP  string `json:"sdp"`
    }

    if err := json.Unmarshal([]byte(msg.SDP), &sdpObj); err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to parse SDP: %v", err))
        return
    }

    if msg.Type == "offer" {
        if err := c.setupPeerConnection(); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to setup peer connection: %v", err))
            return
        }

        offer := webrtc.SessionDescription{
            Type: webrtc.SDPTypeOffer,
            SDP:  sdpObj.SDP,
        }

        if err := c.webrtc.peerConn.SetRemoteDescription(offer); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
            return
        }

        answer, err := c.webrtc.peerConn.CreateAnswer(nil)
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to create answer: %v", err))
            return
        }

        if err := c.webrtc.peerConn.SetLocalDescription(answer); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
            return
        }

        answerJSON, err := json.Marshal(struct {
            Type string `json:"type"`
            SDP  string `json:"sdp"`
        }{
            Type: answer.Type.String(),
            SDP:  answer.SDP,
        })
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to marshal answer: %v", err))
            return
        }

        err = c.SendMessage(Message{
            Type:      "answer",
            PeerToken: c.webrtc.peerToken,
            SDP:       string(answerJSON),
        })
        if err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to send answer: %v", err))
        }
    } else if msg.Type == "answer" {
        answer := webrtc.SessionDescription{
            Type: webrtc.SDPTypeAnswer,
            SDP:  sdpObj.SDP,
        }

        if err := c.webrtc.peerConn.SetRemoteDescription(answer); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to set remote description: %v", err))
        }
    }
}

func (c *Client) handleFileInfo(info map[string]interface{}) {
    if c.webrtc.receiveTransfer.inProgress {
        c.ui.ShowError("Cannot receive file: Download already in progress")
        return
    }

    fileInfo, ok := info["info"].(map[string]interface{})
    if !ok {
        c.ui.ShowError("Invalid file info format")
        return
    }

    name, _ := fileInfo["name"].(string)
    size, _ := fileInfo["size"].(float64)
    md5, _ := fileInfo["md5"].(string)

    // Create downloads directory if it doesn't exist
    downloadDir := "downloads"
    os.MkdirAll(downloadDir, 0755)

    filePath := filepath.Join(downloadDir, name)
    file, err := os.Create(filePath)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to create file: %v", err))
        return
    }

    totalChunks := int(math.Ceil(float64(size) / float64(maxChunkSize)))
    now := time.Now()

    // Setup new receive transfer
    c.webrtc.receiveTransfer = transferState{
        inProgress: true,
        startTime:  now,
        lastUpdate: now,
        fileTransfer: &FileTransfer{
            FileInfo: &FileInfo{
                Name: name,
                Size: int64(size),
                MD5:  md5,
            },
            file:     file,
            filePath: filePath,
        },
        chunks: make([][]byte, totalChunks),
    }

    c.ui.UpdateTransferProgress(fmt.Sprintf("⬇ %s [0%%] (0/s)", name), "receive")
}
