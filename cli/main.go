package main

import (
	"encoding/base64"
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
dataChannel, err := peerConn.CreateDataChannel("p2pftp", nil)
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
if !msg.IsString {
c.ui.ShowError("Received binary data - not supported")
return
}

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

case "file-data":
if c.webrtc.fileInfo == nil {
c.ui.ShowError("Received file data without file info")
return
}

data, err := base64.StdEncoding.DecodeString(dataMsg.Content)
if err != nil {
c.ui.ShowError(fmt.Sprintf("Failed to decode file chunk: %v", err))
return
}

c.webrtc.receiveBuffer = append(c.webrtc.receiveBuffer, data)
c.webrtc.receivedSize += int64(len(data))

// Show progress
percentage := int((float64(c.webrtc.receivedSize) / float64(c.webrtc.fileInfo.Size)) * 100)
c.ui.ShowFileTransfer(fmt.Sprintf("Receiving %s (%d/%d bytes) - %d%%",
c.webrtc.fileInfo.Name,
c.webrtc.receivedSize,
c.webrtc.fileInfo.Size,
percentage))

case "file-complete":
if c.webrtc.fileInfo == nil {
c.ui.ShowError("Received file complete without file info")
return
}

// Combine all chunks
allData := make([]byte, 0, c.webrtc.receivedSize)
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

// Send file info message first
infoMsg := struct {
Type string   `json:"type"`
Info FileInfo `json:"info"`
}{
Type: "file-info",
Info: FileInfo{
Name: fileInfo.Name(),
Size: fileSize,
Type: "", // Not critical for CLI
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

// Send file in chunks
buffer := make([]byte, maxChunkSize)
totalSent := int64(0)

for {
n, err := file.Read(buffer)
if err == io.EOF {
break
}
if err != nil {
return fmt.Errorf("failed to read file: %v", err)
}

chunk := buffer[:n]
fileData := base64.StdEncoding.EncodeToString(chunk)

dataMsg := struct {
Type    string   `json:"type"`
Content string   `json:"content"`
Info    FileInfo `json:"info"`
}{
Type:    "file-data",
Content: fileData,
Info: FileInfo{
Name: fileInfo.Name(),
Size: fileSize,
},
}

dataJSON, err := json.Marshal(dataMsg)
if err != nil {
return fmt.Errorf("failed to marshal file chunk: %v", err)
}

err = c.webrtc.dataChannel.SendText(string(dataJSON))
if err != nil {
return fmt.Errorf("failed to send file chunk: %v", err)
}

totalSent += int64(n)
percentage := int((float64(totalSent) / float64(fileSize)) * 100)
c.ui.ShowFileTransfer(fmt.Sprintf("Sending %s (%d/%d bytes) - %d%%",
fileInfo.Name(), totalSent, fileSize, percentage))

// Small delay to prevent overwhelming the channel
time.Sleep(10 * time.Millisecond)
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
