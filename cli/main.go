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

// Helper struct for data channel messages
type DataChannelMessage struct {
    Type    string   `json:"type"`
    Content string   `json:"content"`
    Info    FileInfo `json:"info"`
}

type FileTransfer struct {
    *FileInfo
    file     *os.File
    filePath string
}

const (
    maxChunkSize = 65536 // 64KB chunks
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
            var dataMsg DataChannelMessage
            if err := json.Unmarshal(msg.Data, &dataMsg); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse message: %v", err))
                return
            }
            c.handleDataMessage(dataMsg)
        } else {
            c.handleBinaryData(msg.Data)
        }
    })
}

func (c *Client) handleDataMessage(msg DataChannelMessage) {
    switch msg.Type {
    case "message":
        c.ui.ShowChat(c.webrtc.peerToken, msg.Content)
    case "file-info":
        c.handleFileInfo(msg.Info)
    case "file-complete":
        c.handleFileComplete()
    }
}

func (c *Client) handleFileInfo(info FileInfo) {
    downloadDir := "downloads"
    if err := os.MkdirAll(downloadDir, 0755); err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to create downloads directory: %v", err))
        return
    }

    filePath := filepath.Join(downloadDir, info.Name)
    file, err := os.Create(filePath)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to create file: %v", err))
        return
    }

    c.webrtc.fileTransfer = &FileTransfer{
        FileInfo: &info,
        file:     file,
        filePath: filePath,
    }
    c.webrtc.chunks = make([][]byte, int(math.Ceil(float64(info.Size)/float64(maxChunkSize))))
    c.webrtc.receivedSize = 0
    c.webrtc.startTime = time.Now()

    c.ui.ShowFileTransfer(fmt.Sprintf("Receiving file: %s (0/%d bytes)", info.Name, info.Size))
}

func (c *Client) handleBinaryData(data []byte) {
    if c.webrtc.fileTransfer == nil || c.webrtc.fileTransfer.file == nil {
        return
    }

    chunkIndex := int(c.webrtc.receivedSize / int64(maxChunkSize))
    if chunkIndex >= len(c.webrtc.chunks) {
        return
    }

    c.webrtc.chunks[chunkIndex] = make([]byte, len(data))
    copy(c.webrtc.chunks[chunkIndex], data)
    c.webrtc.receivedSize += int64(len(data))

    percentage := int((float64(c.webrtc.receivedSize) / float64(c.webrtc.fileTransfer.Size)) * 100)
    c.ui.ShowFileTransfer(fmt.Sprintf("Receiving %s (%d/%d bytes) - %d%%",
        c.webrtc.fileTransfer.Name,
        c.webrtc.receivedSize,
        c.webrtc.fileTransfer.Size,
        percentage))
}

func (c *Client) handleFileComplete() {
    if c.webrtc.fileTransfer == nil || c.webrtc.fileTransfer.file == nil {
        return
    }

    for i, chunk := range c.webrtc.chunks {
        if chunk == nil {
            c.ui.ShowError(fmt.Sprintf("Missing chunk %d/%d", i+1, len(c.webrtc.chunks)))
            c.webrtc.fileTransfer.file.Close()
            return
        }

        if _, err := c.webrtc.fileTransfer.file.Write(chunk); err != nil {
            c.ui.ShowError(fmt.Sprintf("Failed to write chunk %d: %v", i+1, err))
            c.webrtc.fileTransfer.file.Close()
            return
        }
    }

    c.webrtc.fileTransfer.file.Close()
    c.ui.ShowFileTransfer(fmt.Sprintf("Saved file to: %s", c.webrtc.fileTransfer.filePath))
    c.webrtc.fileTransfer = nil
    c.webrtc.chunks = nil
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

    // Calculate MD5
    md5Hash, err := calculateMD5(filePath)
    if err != nil {
        c.ui.ShowError(fmt.Sprintf("Failed to calculate MD5: %v", err))
        md5Hash = "" // Continue without MD5
    }

    infoMsg := DataChannelMessage{
        Type: "file-info",
        Info: FileInfo{
            Name: info.Name(),
            Size: info.Size(),
            MD5:  md5Hash,
        },
    }

    infoJSON, err := json.Marshal(infoMsg)
    if err != nil {
        return fmt.Errorf("failed to marshal file info: %v", err)
    }

    if err := c.webrtc.dataChannel.SendText(string(infoJSON)); err != nil {
        return fmt.Errorf("failed to send file info: %v", err)
    }

    // Send file in chunks
    buffer := make([]byte, maxChunkSize)
    totalSent := int64(0)
    startTime := time.Now()

    for {
        n, err := file.Read(buffer)
        if err == io.EOF {
            break
        }
        if err != nil {
            return fmt.Errorf("failed to read file: %v", err)
        }

        err = c.webrtc.dataChannel.Send(buffer[:n])
        if err != nil {
            return fmt.Errorf("failed to send chunk: %v", err)
        }

        totalSent += int64(n)
        percentage := int((float64(totalSent) / float64(info.Size())) * 100)
        rate := float64(totalSent) / time.Since(startTime).Seconds() / 1024 // KB/s
        c.ui.ShowFileTransfer(fmt.Sprintf("Sending %s (%d/%d bytes) - %d%% (%.1f KB/s)",
            info.Name(), totalSent, info.Size(), percentage, rate))
    }

    completeMsg := DataChannelMessage{Type: "file-complete"}
    completeJSON, err := json.Marshal(completeMsg)
    if err != nil {
        return fmt.Errorf("failed to marshal complete message: %v", err)
    }

    if err := c.webrtc.dataChannel.SendText(string(completeJSON)); err != nil {
        return fmt.Errorf("failed to send complete message: %v", err)
    }

    return nil
}

func (c *Client) SendChat(text string) error {
    if !c.webrtc.connected {
        return fmt.Errorf("not connected to peer")
    }

    msg := DataChannelMessage{
        Type:    "message",
        Content: text,
    }

    msgJSON, err := json.Marshal(msg)
    if err != nil {
        return fmt.Errorf("failed to marshal message: %v", err)
    }

    if err := c.webrtc.dataChannel.SendText(string(msgJSON)); err != nil {
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

            err = c.webrtc.peerConn.SetLocalDescription(offer)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to set local description: %v", err))
                continue
            }

            offerJSON, err := json.Marshal(offer)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to marshal offer: %v", err))
                continue
            }

            err = c.SendMessage(Message{
                Type:      "offer",
                PeerToken: c.webrtc.peerToken,
                SDP:       string(offerJSON),
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

            var offer webrtc.SessionDescription
            if err := json.Unmarshal([]byte(msg.SDP), &offer); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse offer: %v", err))
                continue
            }

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

            answerJSON, err := json.Marshal(answer)
            if err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to marshal answer: %v", err))
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
            var answer webrtc.SessionDescription
            if err := json.Unmarshal([]byte(msg.SDP), &answer); err != nil {
                c.ui.ShowError(fmt.Sprintf("Failed to parse answer: %v", err))
                continue
            }

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
