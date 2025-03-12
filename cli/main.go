package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
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
MD5  string `json:"md5,omitempty"` // MD5 hash for file integrity validation
}

const (
maxChunkSize = 16384 // 16KB chunks for file transfer
)

type WebRTCState struct {
peerToken     string
isInitiator   bool
connected     bool
peerConn     *webrtc.PeerConnection
dataChannel  *webrtc.DataChannel
// File transfer state
receiveBuffer [][]byte
receivedSize int64
fileInfo     *FileInfo
}

type Client struct {
conn     *websocket.Conn
token    string
ui       *UI
webrtc   *WebRTCState
}

// Calculate MD5 hash of a file with chunking for large files
func calculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file for MD5 calculation: %v", err)
	}
	defer file.Close()

	// Use a buffer to read the file in chunks
	hash := md5.New()
	buffer := make([]byte, 32*1024) // 32KB chunks for MD5 calculation
	
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			hash.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("error reading file for MD5 calculation: %v", err)
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Calculate MD5 hash of a byte slice
func calculateMD5FromBytes(data []byte) string {
	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:])
}

func main() {
addr := flag.String("addr", "localhost:8089", "server address")
flag.Parse()

// Create WebSocket URL
u := url.URL{Scheme: "wss", Host: *addr, Path: "/ws"}
log.Printf("Connecting to %s...", u.String())

// Connect to WebSocket server
conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
if err != nil {
log.Fatal("WebSocket dial error:", err)
}
defer conn.Close()
log.Printf("Successfully connected to server")

client := &Client{
conn:    conn,
webrtc:  &WebRTCState{},
}

// Create and start UI
ui := NewUI(client)
client.ui = ui

// Start message handler
go client.handleMessages()

// Run UI
if err := ui.Run(); err != nil {
fmt.Printf("Error running UI: %v\n", err)
}
}

func (c *Client) setupPeerConnection() error {
config := webrtc.Configuration{
ICEServers: []webrtc.ICEServer{
{
URLs: []string{"stun:stun.l.google.com:19302"},
},
},
}

peerConn, err := webrtc.NewPeerConnection(config)
if err != nil {
return fmt.Errorf("failed to create peer connection: %v", err)
}

peerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
if candidate != nil {
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
}
})

if c.webrtc.isInitiator {
    // Configure data channel for reliability with large files
    dataChannelConfig := &webrtc.DataChannelInit{
        Ordered:           new(bool),   // Guarantee order of messages
        MaxRetransmits:    new(uint16), // Increased retransmits for reliability
        MaxPacketLifeTime: new(uint16), // 3 seconds max packet lifetime
    }
    *dataChannelConfig.Ordered = true
    *dataChannelConfig.MaxRetransmits = 10
    *dataChannelConfig.MaxPacketLifeTime = 3000
    
    dataChannel, err := peerConn.CreateDataChannel("p2pftp", dataChannelConfig)
    if err != nil {
        return fmt.Errorf("failed to create data channel: %v", err)
    }
    c.setupDataChannel(dataChannel)
} else {
    peerConn.OnDataChannel(func(channel *webrtc.DataChannel) {
        c.setupDataChannel(channel)
    })
}

c.webrtc.peerConn = peerConn
return nil
}

func (c *Client) setupDataChannel(channel *webrtc.DataChannel) {
c.webrtc.dataChannel = channel

channel.OnOpen(func() {
c.ui.LogDebug("Data channel opened")
c.webrtc.connected = true
})

channel.OnClose(func() {
c.ui.LogDebug("Data channel closed")
c.webrtc.connected = false
})

channel.OnMessage(func(msg webrtc.DataChannelMessage) {
    if msg.IsString {
        var dataMsg struct {
            Type    string   `json:"type"`
            Content string   `json:"content"`
            Info    FileInfo `json:"info"`
        }

        if err := json.Unmarshal([]byte(msg.Data), &dataMsg); err == nil {
            switch dataMsg.Type {
            case "message":
                c.ui.ShowChat(c.webrtc.peerToken, dataMsg.Content)

            case "file-info":
                c.webrtc.fileInfo = &dataMsg.Info
                c.webrtc.receiveBuffer = make([][]byte, 0)
                c.webrtc.receivedSize = 0
                c.ui.ShowFileTransfer(fmt.Sprintf("Receiving file: %s (0/%d bytes)", dataMsg.Info.Name, dataMsg.Info.Size))

            case "file-complete":
                if c.webrtc.fileInfo == nil {
                    c.ui.ShowError("Received file complete without file info")
                    return
                }

                // Combine all chunks with size verification
                expectedSize := c.webrtc.fileInfo.Size
                actualSize := c.webrtc.receivedSize
                
                if actualSize != expectedSize {
                    c.ui.ShowError(fmt.Sprintf("File size mismatch: expected %d bytes, got %d bytes", 
                        expectedSize, actualSize))
                    c.ui.ShowFileTransfer(fmt.Sprintf("⚠️ Warning: Received file size (%d bytes) doesn't match expected size (%d bytes)", 
                        actualSize, expectedSize))
                }
                
                // Pre-allocate buffer with exact size to avoid memory issues
                allData := make([]byte, 0, actualSize)
                for _, chunk := range c.webrtc.receiveBuffer {
                    allData = append(allData, chunk...)
                }

                // Create downloads directory if it doesn't exist
                downloadDir := "downloads"
                if err := os.MkdirAll(downloadDir, 0755); err != nil {
                    c.ui.ShowError(fmt.Sprintf("Failed to create downloads directory: %v", err))
                    return
                }

                // Save file
                filePath := filepath.Join(downloadDir, c.webrtc.fileInfo.Name)
                err := os.WriteFile(filePath, allData, 0644)
                if err != nil {
                    c.ui.ShowError(fmt.Sprintf("Failed to save file: %v", err))
                    return
                }

                // Validate MD5 checksum if provided
                if c.webrtc.fileInfo.MD5 != "" {
                    receivedMD5 := calculateMD5FromBytes(allData)
                    if receivedMD5 != c.webrtc.fileInfo.MD5 {
                        c.ui.ShowError(fmt.Sprintf("File integrity check failed! MD5 mismatch: expected %s, got %s", 
                            c.webrtc.fileInfo.MD5, receivedMD5))
                        c.ui.ShowFileTransfer(fmt.Sprintf("⚠️ Warning: File may be corrupted: %s", filePath))
                    } else {
                        c.ui.ShowFileTransfer(fmt.Sprintf("✓ File integrity verified (MD5: %s)", receivedMD5))
                    }
                }

                c.ui.ShowFileTransfer(fmt.Sprintf("Saved file from peer to: %s", filePath))

                // Reset file transfer state
                c.webrtc.fileInfo = nil
                c.webrtc.receiveBuffer = nil
                c.webrtc.receivedSize = 0
            }
        } else {
            // Just a plain text message
            c.ui.ShowChat(c.webrtc.peerToken, string(msg.Data))
        }
    } else {
        // Binary data - file chunk
        if c.webrtc.fileInfo == nil {
            c.ui.ShowError("Received file data without file info")
            return
        }

        // Make a copy of the data since it might be reused by the WebRTC implementation
        data := make([]byte, len(msg.Data))
        copy(data, msg.Data)
        
        c.webrtc.receiveBuffer = append(c.webrtc.receiveBuffer, data)
        c.webrtc.receivedSize += int64(len(data))

        // Show progress
        percentage := int((float64(c.webrtc.receivedSize) / float64(c.webrtc.fileInfo.Size)) * 100)
        c.ui.ShowFileTransfer(fmt.Sprintf("Receiving %s (%d/%d bytes) - %d%%",
            c.webrtc.fileInfo.Name,
            c.webrtc.receivedSize,
            c.webrtc.fileInfo.Size,
            percentage))
    }
})
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

func (c *Client) disconnectPeer() {
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

func (c *Client) SendChat(text string) error {
if !c.webrtc.connected || c.webrtc.dataChannel == nil {
return fmt.Errorf("not connected to peer")
}

chatMsg := struct {
Type    string `json:"type"`
Content string `json:"content"`
}{
Type:    "message",
Content: text,
}

chatJSON, err := json.Marshal(chatMsg)
if err != nil {
return fmt.Errorf("failed to marshal chat message: %v", err)
}

err = c.webrtc.dataChannel.SendText(string(chatJSON))
if err != nil {
return fmt.Errorf("failed to send chat message: %v", err)
}
return nil
}

func (c *Client) SendFile(filePath string) error {
if !c.webrtc.connected || c.webrtc.dataChannel == nil {
return fmt.Errorf("not connected to peer")
}

file, err := os.Open(filePath)
if err != nil {
return fmt.Errorf("failed to open file: %v", err)
}
defer file.Close()

fileInfo, err := file.Stat()
if err != nil {
return fmt.Errorf("failed to get file info: %v", err)
}

fileSize := fileInfo.Size()

// Calculate MD5 hash for file integrity validation
c.ui.ShowFileTransfer("Calculating MD5 checksum...")
md5Hash, err := calculateMD5(filePath)
if err != nil {
c.ui.ShowError(fmt.Sprintf("Failed to calculate MD5: %v", err))
// Continue without MD5 validation
md5Hash = ""
} else {
c.ui.ShowFileTransfer(fmt.Sprintf("File MD5: %s", md5Hash))
}

// Send file info message first with MD5 hash
infoMsg := struct {
Type string   `json:"type"`
Info FileInfo `json:"info"`
}{
Type: "file-info",
Info: FileInfo{
Name: fileInfo.Name(),
Size: fileSize,
Type: "", // Not critical for CLI
MD5:  md5Hash,
},
}

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
lastProgressUpdate := time.Now()

for {
    n, err := file.Read(buffer)
    if err == io.EOF {
        break
    }
    if err != nil {
        return fmt.Errorf("failed to read file: %v", err)
    }

    // Flow control: wait if the buffer is getting full
    for c.webrtc.dataChannel.BufferedAmount() > maxChunkSize*8 {
        time.Sleep(50 * time.Millisecond)
    }

    // Send chunk as binary data
    err = c.webrtc.dataChannel.Send(buffer[:n])
    if err != nil {
        return fmt.Errorf("failed to send file chunk: %v", err)
    }

    totalSent += int64(n)
    
    // Update progress less frequently for large files to avoid UI flooding
    if time.Since(lastProgressUpdate) > 200*time.Millisecond {
        percentage := int((float64(totalSent) / float64(fileSize)) * 100)
        c.ui.ShowFileTransfer(fmt.Sprintf("Sending %s (%d/%d bytes) - %d%%",
            fileInfo.Name(), totalSent, fileSize, percentage))
        lastProgressUpdate = time.Now()
    }
}

// Send file complete message
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

c.ui.ShowFileTransfer(fmt.Sprintf("Sent file: %s", filePath))
return nil
}
