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
	MD5  string `json:"md5,omitempty"` // MD5 hash for file integrity validation
}

// Extended file info with local file handling
type FileTransfer struct {
	*FileInfo
	file     *os.File    // File handle for writing chunks
	filePath string      // Path where file is being written
}

const (
	maxChunkSize = 65536 // 64KB chunks to match web UI
)

type WebRTCState struct {
	peerToken     string
	isInitiator   bool
	connected     bool
	peerConn      *webrtc.PeerConnection
	dataChannel   *webrtc.DataChannel
	// File transfer state
	receivedSize  int64
	fileTransfer  *FileTransfer
	startTime     time.Time // Added for tracking transfer start time
	chunks       [][]byte   // Buffer for receiving file chunks in order
	totalChunks  int       // Total number of expected chunks
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
	// Use multiple STUN servers and public TURN servers for better NAT traversal
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// Primary STUN servers
			{
				URLs: []string{
					"stun:stun.l.google.com:19302",
					"stun:stun1.l.google.com:19302",
				},
			},
			// Public TURN servers for NAT traversal with TCP fallback
			{
				URLs: []string{
					"turn:openrelay.metered.ca:80",
					"turn:openrelay.metered.ca:443",
					"turn:openrelay.metered.ca:443?transport=tcp",
				},
				Username:   "openrelayproject",
				Credential: "openrelayproject",
			},
		},
		ICETransportPolicy:    webrtc.ICETransportPolicyAll,
		BundlePolicy:          webrtc.BundlePolicyMaxBundle,
		ICECandidatePoolSize:  2,
	}

	c.ui.LogDebug("Setting up WebRTC with enhanced ICE configuration")

	peerConn, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %v", err)
	}

	// Monitor connection state changes
	peerConn.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		c.ui.LogDebug(fmt.Sprintf("WebRTC connection state changed to: %s", state))
		if state == webrtc.PeerConnectionStateFailed {
			c.ui.ShowError("WebRTC connection failed - try reconnecting")
			c.disconnectPeer()
		}
	})

	// Monitor SCTP transport
	go func() {
		// Wait for SCTP transport to be established
		for peerConn.SCTP() == nil {
			time.Sleep(100 * time.Millisecond)
		}
		c.ui.LogDebug(fmt.Sprintf("SCTP transport established with state: %s", peerConn.SCTP().State()))
	}()

	// Log signaling state changes
	peerConn.OnSignalingStateChange(func(state webrtc.SignalingState) {
		c.ui.LogDebug(fmt.Sprintf("Signaling state changed to: %s", state))
	})

	// Monitor ICE connection state
	peerConn.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		c.ui.LogDebug(fmt.Sprintf("ICE connection state changed to: %s", state))
		if state == webrtc.ICEConnectionStateFailed {
			c.ui.ShowError("ICE connection failed - try reconnecting")
			// Create new offer to restart ICE
			offer, err := peerConn.CreateOffer(&webrtc.OfferOptions{
				ICERestart: true,
			})
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to create ICE restart offer: %v", err))
				c.disconnectPeer()
				return
			}
			err = peerConn.SetLocalDescription(offer)
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to set local description for ICE restart: %v", err))
				c.disconnectPeer()
				return
			}
			err = c.SendMessage(Message{
				Type:      "offer",
				PeerToken: c.webrtc.peerToken,
				SDP:       string(offer.SDP),
			})
			if err != nil {
				c.ui.ShowError(fmt.Sprintf("Failed to send ICE restart offer: %v", err))
				c.disconnectPeer()
			}
		}
	})

	// Monitor ICE gathering state
	peerConn.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
		c.ui.LogDebug(fmt.Sprintf("ICE gathering state changed to: %s", state))
	})

	// Send ICE candidates to peer
	peerConn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		c.ui.LogDebug("Got ICE candidate")
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
		// Configure data channel to match web UI settings
		ordered := true
		maxRetransmits := uint16(30)
		dataChannelConfig := &webrtc.DataChannelInit{
			Ordered:        &ordered,
			MaxRetransmits: &maxRetransmits,
		}

		c.ui.LogDebug("Creating data channel with ordered delivery and retransmits")
		dataChannel, err := peerConn.CreateDataChannel("p2pftp", dataChannelConfig)
		if err != nil {
			return fmt.Errorf("failed to create data channel: %v", err)
		}

		// Log data channel state
		c.ui.LogDebug(fmt.Sprintf("Data channel created with state: %s", dataChannel.ReadyState().String()))

		// Set buffered amount low threshold to help with flow control
		dataChannel.SetBufferedAmountLowThreshold(maxChunkSize * 4)
		dataChannel.OnBufferedAmountLow(func() {
			c.ui.LogDebug("Data channel buffer low event")
		})

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

	// Log initial state
	c.ui.LogDebug(fmt.Sprintf("Setting up data channel (ID: %d, Label: %s, State: %s)", 
		channel.ID(), channel.Label(), channel.ReadyState().String()))

	// Wait for data channel to be ready
	channel.OnOpen(func() {
		c.ui.LogDebug(fmt.Sprintf("Data channel opened (ID: %d, Label: %s)", channel.ID(), channel.Label()))

		// Log data channel configuration
		c.ui.LogDebug(fmt.Sprintf("Data channel config - Ordered: %v, MaxRetransmits: %v, State: %s", 
			channel.Ordered(), channel.MaxRetransmits(), channel.ReadyState().String()))

		// Log negotiated vs non-negotiated
		c.ui.LogDebug(fmt.Sprintf("Data channel negotiated: %v, Protocol: %s", 
			channel.Negotiated(), channel.Protocol()))

		// Log SCTP transport state
		if transport := c.webrtc.peerConn.SCTP(); transport != nil {
			c.ui.LogDebug(fmt.Sprintf("SCTP transport state on data channel open: %s", transport.State()))
		} else {
			c.ui.LogDebug("SCTP transport not yet available")
		}

		// Only set connected after all checks pass
		c.webrtc.connected = true
		c.ui.LogDebug("Data channel ready for transfer")
	})

	channel.OnClose(func() {
		c.ui.LogDebug(fmt.Sprintf("Data channel closed (Last state: %s)", channel.ReadyState().String()))

		// Only disconnect if we were previously connected
		if c.webrtc.connected {
			c.webrtc.connected = false
			
			// Close file if transfer was in progress
			if c.webrtc.fileTransfer != nil && c.webrtc.fileTransfer.file != nil {
				c.ui.LogDebug(fmt.Sprintf("Closing incomplete file transfer (%d/%d bytes received)", 
					c.webrtc.receivedSize, c.webrtc.fileTransfer.Size))
				c.webrtc.fileTransfer.file.Close()
				c.webrtc.fileTransfer = nil
			}

			// Try to reconnect if this wasn't an intentional close
			if c.webrtc.peerConn != nil && c.webrtc.peerConn.ConnectionState() != webrtc.PeerConnectionStateClosed {
				c.ui.LogDebug("Data channel closed unexpectedly, attempting to recreate")
				// Create new data channel with same config
				ordered := true
				maxRetransmits := uint16(30)
				dataChannelConfig := &webrtc.DataChannelInit{
					Ordered:        &ordered,
					MaxRetransmits: &maxRetransmits,
				}
				if newChannel, err := c.webrtc.peerConn.CreateDataChannel("p2pftp", dataChannelConfig); err == nil {
					c.setupDataChannel(newChannel)
				} else {
					c.ui.ShowError(fmt.Sprintf("Failed to recreate data channel: %v", err))
				}
			}
		}
	})

	channel.OnError(func(err error) {
		c.ui.LogDebug(fmt.Sprintf("Data channel error: %v", err))
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
					// Create downloads directory if it doesn't exist
					downloadDir := "downloads"
					if err := os.MkdirAll(downloadDir, 0755); err != nil {
						c.ui.ShowError(fmt.Sprintf("Failed to create downloads directory: %v", err))
						return
					}

					// Create file for writing chunks
					filePath := filepath.Join(downloadDir, dataMsg.Info.Name)
					file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
					if err != nil {
						c.ui.ShowError(fmt.Sprintf("Failed to create file: %v", err))
						return
					}

					// Initialize file transfer state with pre-allocated chunk buffer
					totalChunks := int(math.Ceil(float64(dataMsg.Info.Size) / float64(maxChunkSize)))
					c.webrtc.chunks = make([][]byte, totalChunks)
					c.webrtc.totalChunks = totalChunks
					c.webrtc.fileTransfer = &FileTransfer{
						FileInfo: &dataMsg.Info,
						file:     file,
						filePath: filePath,
					}
					c.webrtc.receivedSize = 0
					c.webrtc.startTime = time.Now()
					
					c.ui.LogDebug(fmt.Sprintf("Prepared to receive %d chunks", totalChunks))

					c.ui.ShowFileTransfer(fmt.Sprintf("Receiving file: %s (0/%d bytes)", dataMsg.Info.Name, dataMsg.Info.Size))

				case "file-complete", "complete": // Handle both formats for compatibility
					if c.webrtc.fileTransfer == nil || c.webrtc.fileTransfer.file == nil {
						c.ui.ShowError("Received file complete without active transfer")
						return
					}

					// Write all chunks to file in order
					for i, chunk := range c.webrtc.chunks {
						if chunk == nil {
							c.ui.ShowError(fmt.Sprintf("Missing chunk %d/%d", i+1, c.webrtc.totalChunks))
							c.webrtc.fileTransfer.file.Close()
							return
						}
						if _, err := c.webrtc.fileTransfer.file.Write(chunk); err != nil {
							c.ui.ShowError(fmt.Sprintf("Failed to write chunk %d: %v", i+1, err))
							c.webrtc.fileTransfer.file.Close()
							return
						}
					}

					// Close file and clear chunk buffer
					c.webrtc.fileTransfer.file.Close()
					c.webrtc.chunks = nil

					// Verify file size
					expectedSize := c.webrtc.fileTransfer.Size
					actualSize := c.webrtc.receivedSize
					
					if actualSize != expectedSize {
						c.ui.ShowError(fmt.Sprintf("File size mismatch: expected %d bytes, got %d bytes", 
							expectedSize, actualSize))
						c.ui.ShowFileTransfer(fmt.Sprintf("⚠️ Warning: Received file size (%d bytes) doesn't match expected size (%d bytes)", 
							actualSize, expectedSize))
					}

					// Validate MD5 checksum if provided
					if c.webrtc.fileTransfer.MD5 != "" {
						if receivedMD5, err := calculateMD5(c.webrtc.fileTransfer.filePath); err == nil {
							if receivedMD5 != c.webrtc.fileTransfer.MD5 {
								c.ui.ShowError(fmt.Sprintf("File integrity check failed! MD5 mismatch: expected %s, got %s", 
									c.webrtc.fileTransfer.MD5, receivedMD5))
								c.ui.ShowFileTransfer(fmt.Sprintf("⚠️ Warning: File may be corrupted: %s", c.webrtc.fileTransfer.filePath))
							} else {
								c.ui.ShowFileTransfer(fmt.Sprintf("✓ File integrity verified (MD5: %s)", receivedMD5))
							}
						} else {
							c.ui.ShowError(fmt.Sprintf("Failed to calculate received file MD5: %v", err))
						}
					}

					c.ui.ShowFileTransfer(fmt.Sprintf("Saved file from peer to: %s", c.webrtc.fileTransfer.filePath))

					// Reset file transfer state
					c.webrtc.fileTransfer = nil
					c.webrtc.receivedSize = 0
				}
			} else {
				// Just a plain text message
				c.ui.ShowChat(c.webrtc.peerToken, string(msg.Data))
			}
		} else {
			// Binary data - file chunk
			if c.webrtc.fileTransfer == nil || c.webrtc.fileTransfer.file == nil {
				c.ui.ShowError("Received file data without active transfer")
				return
			}

			// Ensure data channel is still open
			if c.webrtc.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
				c.ui.ShowError("Data channel closed during transfer")
				return
			}

			// Calculate chunk index based on received size
			chunkIndex := int(c.webrtc.receivedSize / int64(maxChunkSize))
			if chunkIndex >= c.webrtc.totalChunks {
				c.ui.ShowError("Received more chunks than expected")
				return
			}

			// Store chunk in buffer
			c.webrtc.chunks[chunkIndex] = make([]byte, len(msg.Data))
			copy(c.webrtc.chunks[chunkIndex], msg.Data)

			// Update received size
			c.webrtc.receivedSize += int64(len(msg.Data))

			// Show progress
			percentage := int((float64(c.webrtc.receivedSize) / float64(c.webrtc.fileTransfer.Size)) * 100)
			elapsed := time.Since(c.webrtc.startTime)
			rate := float64(c.webrtc.receivedSize) / elapsed.Seconds()
			rateKB := rate / 1024
			rateStr := fmt.Sprintf("%.1f KB/s", rateKB)
			c.ui.ShowFileTransfer(fmt.Sprintf("Receiving %s (%d/%d bytes) - %d%% (%s)",
				c.webrtc.fileTransfer.Name,
				c.webrtc.receivedSize,
				c.webrtc.fileTransfer.Size,
				percentage,
				rateStr))
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

// SendMessage sends a message through the WebSocket connection
func (c *Client) SendMessage(msg Message) error {
	err := c.conn.WriteJSON(msg)
	if err != nil {
		c.ui.ShowError("Send failed: " + err.Error())
		return err
	}
	return nil
}

// Connect initiates a connection to a peer
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

// Accept accepts a connection request from a peer
func (c *Client) Accept(peerToken string) error {
	if c.webrtc.connected {
		return fmt.Errorf("already connected to a peer")
	}
	c.webrtc = &WebRTCState{peerToken: peerToken, isInitiator: false}
	return c.SendMessage(Message{Type: "accept", PeerToken: peerToken})
}

// Reject rejects a connection request from a peer
func (c *Client) Reject(peerToken string) error {
	if c.webrtc.connected {
		c.disconnectPeer()
	}
	return c.SendMessage(Message{Type: "reject", PeerToken: peerToken})
}

// SendChat sends a chat message to the connected peer
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

// SendFile sends a file to the connected peer
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
	startTime := time.Now()
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

		// Flow control: wait if buffer is getting too full
		// Match web UI's buffering strategy
		for c.webrtc.dataChannel.BufferedAmount() > maxChunkSize*16 {
			time.Sleep(100 * time.Millisecond)
			
			// Check connection state
			if c.webrtc.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
				return fmt.Errorf("data channel closed during transfer")
			}
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
			elapsed := time.Since(startTime)
			rate := float64(totalSent) / elapsed.Seconds()
			rateKB := rate / 1024
			rateStr := fmt.Sprintf("%.1f KB/s", rateKB)
			c.ui.ShowFileTransfer(fmt.Sprintf("Sending %s (%d/%d bytes) - %d%% (%s)",
				fileInfo.Name(), totalSent, fileSize, percentage, rateStr))
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
