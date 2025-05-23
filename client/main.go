package main

import (
	"bufio"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wltechblog/p2pftp/client/webrtc"
)

var (
	debug      = flag.Bool("debug", false, "Enable debug logging")
	logFile    = flag.String("logfile", "p2pftp-debug.log", "Path to debug log file")
	serverURL  = flag.String("server", "p2p.teamworkapps.com", "Signaling server hostname (port 443 will be used)")
	connectURL = flag.String("url", "", "Full connection URL (e.g., https://p2p.teamworkapps.com/?token=abcd1234)")
	debugLog   *log.Logger
)

// FileInfo represents metadata about a file
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

// setupLogger creates a logger that writes to both stderr and a log file
func setupLogger() (*log.Logger, error) {
	if !*debug {
		return log.New(io.Discard, "", 0), nil
	}

	// Create log file
	f, err := os.OpenFile(*logFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		return nil, fmt.Errorf("error opening log file: %v", err)
	}

	// Create multi-writer to write to both stderr and file
	multiWriter := io.MultiWriter(os.Stderr, f)

	// Create logger with timestamp and file info
	return log.New(multiWriter, "DEBUG: ", log.Ltime|log.Lshortfile), nil
}

// parseConnectionURL extracts the server URL and token from a connection URL
func parseConnectionURL(urlStr string) (string, string, error) {
	if urlStr == "" {
		return "", "", nil
	}

	// Safe debug logging
	logDebug := func(format string, args ...interface{}) {
		if debugLog != nil {
			debugLog.Printf(format, args...)
		}
	}

	logDebug("Parsing connection URL: %s", urlStr)

	// Handle hostname only (no path or protocol)
	if !strings.Contains(urlStr, "://") && !strings.Contains(urlStr, "/") {
		// Extract hostname without port
		hostname := urlStr
		if strings.Contains(urlStr, ":") {
			hostname = strings.Split(urlStr, ":")[0]
		}
		result := "https://" + hostname
		logDebug("Hostname only URL, converted to: %s", result)
		return result, "", nil
	}

	// Add https:// prefix if missing but has path
	if !strings.Contains(urlStr, "://") && strings.Contains(urlStr, "/") {
		urlStr = "https://" + urlStr
		logDebug("Added https:// prefix: %s", urlStr)
	}

	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %v", err)
	}

	// Extract token if present
	token := parsed.Query().Get("token")

	// Remove query parameters to get base server URL
	parsed.RawQuery = ""
	result := parsed.String()

	logDebug("Parsed URL: %s, Token: %s", result, token)
	return result, token, nil
}

// getWebSocketURL converts HTTP/HTTPS URL to WSS URL
func getWebSocketURL(httpURL string) string {
	// Safe debug logging
	logDebug := func(format string, args ...interface{}) {
		if debugLog != nil {
			debugLog.Printf(format, args...)
		}
	}

	logDebug("Converting URL to WebSocket: %s", httpURL)

	// Handle URLs without protocol
	if !strings.Contains(httpURL, "://") {
		// Extract hostname (remove port if present)
		hostname := httpURL
		if strings.Contains(httpURL, ":") {
			hostname = strings.Split(httpURL, ":")[0]
		}
		// Always use wss:// and port 443
		httpURL = "wss://" + hostname + ":443"
		logDebug("Added protocol and port 443: %s", httpURL)
	} else {
		// Convert HTTP/HTTPS to WSS (always secure)
		httpURL = strings.Replace(httpURL, "http:", "wss:", 1)
		httpURL = strings.Replace(httpURL, "https:", "wss:", 1)
		httpURL = strings.Replace(httpURL, "ws:", "wss:", 1) // Ensure WSS even if WS was specified

		// Parse and ensure port 443
		u, err := url.Parse(httpURL)
		if err == nil {
			u.Host = u.Hostname() + ":443" // Always use port 443
			httpURL = u.String()
		}
		logDebug("Converted to WSS with port 443: %s", httpURL)
	}

	// Parse URL
	u, err := url.Parse(httpURL)
	if err != nil {
		logDebug("Error parsing URL: %v", err)
		return ""
	}

	// Always use /ws path as defined in the server
	u.Path = "/ws"
	logDebug("Set WebSocket path: %s", u.Path)

	result := u.String()
	logDebug("Final WebSocket URL: %s", result)
	return result
}

// CLI represents the command-line interface client
type CLI struct {
	peer             *webrtc.Peer
	wsURL            string
	serverURL        string
	token            string
	peerToken        string
	debugLog         *log.Logger
	connected        bool
	transferMu       sync.Mutex
	fileInfo         *FileInfo
	fileData         []byte
	receiving        bool
	sendChan         chan []byte
	quit             chan struct{}
	lastRequestToken string // Stores the most recent connection request token
	transferComplete bool   // Flag to indicate the sender has sent file-complete
}

// NewCLI creates a new CLI client
func NewCLI(serverURLStr, token string, debug *log.Logger) *CLI {
	return &CLI{
		wsURL:            getWebSocketURL(serverURLStr),
		serverURL:        serverURLStr,
		token:            token,
		debugLog:         debug,
		connected:        false,
		quit:             make(chan struct{}),
		sendChan:         make(chan []byte, 64),
		lastRequestToken: "",
		transferComplete: false,
	}
}

// Start initializes the CLI client and connects to the server
func (c *CLI) Start() error {
	fmt.Println("P2PFTP CLI Client")
	fmt.Println("=================")
	fmt.Printf("Server: %s\n", c.serverURL)

	// Create peer
	peer, err := webrtc.NewPeer(c.debugLog)
	if err != nil {
		return fmt.Errorf("failed to create peer: %v", err)
	}
	c.peer = peer

	// Set up handlers
	peer.SetTokenHandler(func(token string) {
		c.token = token
		fmt.Printf("\nYour token: %s\n", token)
		fmt.Printf("Your connection link: %s/?token=%s\n", c.serverURL, c.token)
	})

	peer.SetErrorHandler(func(errMsg string) {
		fmt.Printf("\nServer error: %s\n", errMsg)
	})

	peer.SetMessageHandler(func(msg string) {
		fmt.Printf("\n<%s> %s\n", c.peerToken, msg)
		fmt.Print("> ")
	})

	peer.SetStatusHandler(func(status string) {
		// Check for special token storage message
		if strings.HasPrefix(status, "__STORE_REQUEST_TOKEN__:") {
			parts := strings.Split(status, ":")
			if len(parts) == 2 {
				c.lastRequestToken = parts[1]
				c.debugLog.Printf("Stored request token: %s", c.lastRequestToken)
				return // Don't print this special message
			}
		}

		fmt.Printf("\nConnection status: %s\n", status)

		// Update connection status based on peer's IsConnected method
		c.connected = peer.IsConnected()

		// Also check status message for additional connection states
		if strings.Contains(status, "connected") || strings.Contains(status, "Connection state: connected") {
			c.connected = true
		} else if strings.Contains(status, "disconnected") || strings.Contains(status, "failed") || strings.Contains(status, "closed") {
			c.connected = false
		}

		fmt.Printf("Connection state: %v\n", c.connected)
		fmt.Print("> ")
	})

	peer.SetControlHandler(func(data []byte) {
		c.handleControlMessage(data)
	})

	peer.SetDataHandler(func(data []byte) {
		c.handleDataMessage(data)
	})

	// Register with server
	if err := peer.Register(c.wsURL); err != nil {
		return fmt.Errorf("failed to register with server: %v", err)
	}

	fmt.Println("\nType /help for available commands")

	// Start input loop
	go c.inputLoop()

	return nil
}

// handleControlMessage processes control messages from the peer
func (c *CLI) handleControlMessage(data []byte) {
	c.debugLog.Printf("Received control message: %s", string(data))

	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		c.debugLog.Printf("Error parsing control message: %v", err)
		return
	}

	msgType, ok := msg["type"].(string)
	if !ok {
		c.debugLog.Printf("Invalid message format: missing type")
		return
	}

	switch msgType {
	case "file-chunk":
		// Handle file chunk sent via control channel
		c.debugLog.Printf("Received file chunk via control channel")

		// Extract chunk data
		sequence, _ := msg["sequence"].(float64)
		size, _ := msg["size"].(float64)
		dataStr, _ := msg["data"].(string)

		c.debugLog.Printf("Chunk info: sequence=%.0f, size=%.0f", sequence, size)

		// Decode base64 data
		chunkData, err := base64.StdEncoding.DecodeString(dataStr)
		if err != nil {
			c.debugLog.Printf("Error decoding chunk data: %v", err)
			return
		}

		// Create a fake data message with header
		headerData := make([]byte, 8+len(chunkData))

		// Write sequence number (4 bytes)
		seq := int(sequence)
		headerData[0] = byte(seq >> 24)
		headerData[1] = byte(seq >> 16)
		headerData[2] = byte(seq >> 8)
		headerData[3] = byte(seq)

		// Write chunk size (4 bytes)
		chunkSize := len(chunkData)
		headerData[4] = byte(chunkSize >> 24)
		headerData[5] = byte(chunkSize >> 16)
		headerData[6] = byte(chunkSize >> 8)
		headerData[7] = byte(chunkSize)

		// Copy chunk data
		copy(headerData[8:], chunkData)

		// Process the chunk as if it came from the data channel
		c.handleDataMessage(headerData)

	case "capabilities":
		// Handle capabilities message
		maxChunkSize, _ := msg["maxChunkSize"].(float64)
		fmt.Printf("\nPeer supports max chunk size: %.0f bytes\n", maxChunkSize)

		// Send capabilities acknowledgment
		c.sendCapabilitiesAck(int(maxChunkSize))
		fmt.Print("> ")

	case "capabilities-ack":
		// Handle capabilities acknowledgment
		negotiatedSize, _ := msg["negotiatedChunkSize"].(float64)
		fmt.Printf("\nNegotiated chunk size: %.0f bytes\n", negotiatedSize)
		fmt.Print("> ")

	case "file-info":
		// Handle file info message
		info, ok := msg["info"].(map[string]interface{})
		if !ok {
			c.debugLog.Printf("Invalid file-info format")
			return
		}

		name, _ := info["name"].(string)
		size, _ := info["size"].(float64)
		md5, _ := info["md5"].(string)

		c.transferMu.Lock()
		c.fileInfo = &FileInfo{
			Name: name,
			Size: int64(size),
			MD5:  md5,
		}
		c.fileData = make([]byte, 0, int64(size))
		c.receiving = true
		c.transferMu.Unlock()

		fmt.Printf("\nReceiving file: %s (%.0f bytes)\n", name, size)
		fmt.Print("> ")

	case "progress-update":
		// Handle progress update
		bytesReceived, _ := msg["bytesReceived"].(float64)
		highestSequence, _ := msg["highestSequence"].(float64)

		if c.fileInfo != nil {
			percentage := (bytesReceived / float64(c.fileInfo.Size)) * 100
			fmt.Printf("\rProgress: %.1f%% (%.0f/%.0f bytes, sequence: %.0f)",
				percentage, bytesReceived, float64(c.fileInfo.Size), highestSequence)
		}

	case "file-complete":
		// Handle file complete message
		fmt.Printf("\nFile transfer complete, waiting for all chunks...\n")

		c.transferMu.Lock()
		c.transferComplete = true

		// Check if we've received all the data
		if c.fileInfo != nil && int64(len(c.fileData)) == c.fileInfo.Size {
			// We have all the data, proceed with verification and saving
			c.transferMu.Unlock()
			c.saveReceivedFile()
		} else {
			// We're still waiting for chunks, don't verify yet
			c.debugLog.Printf("Received file-complete but only have %d/%d bytes. Waiting for remaining chunks...",
				len(c.fileData), c.fileInfo.Size)
			c.transferMu.Unlock()
		}

		fmt.Print("> ")

	case "file-verified":
		// Handle file verification message
		fmt.Printf("\nFile verified successfully\n")
		fmt.Print("> ")

	case "file-failed":
		// Handle file verification failure
		reason, _ := msg["reason"].(string)
		fmt.Printf("\nFile verification failed: %s\n", reason)
		fmt.Print("> ")

	case "message":
		// Handle chat message
		content, _ := msg["content"].(string)
		fmt.Printf("\n<%s> %s\n", c.peerToken, content)
		fmt.Print("> ")
	}
}

// handleDataMessage processes data messages (file chunks) from the peer
func (c *CLI) handleDataMessage(data []byte) {
	c.debugLog.Printf("handleDataMessage called with %d bytes of data", len(data))

	// Print first few bytes for debugging
	if len(data) > 0 {
		maxBytes := 32
		if len(data) < maxBytes {
			maxBytes = len(data)
		}
		c.debugLog.Printf("First %d bytes: %v", maxBytes, data[:maxBytes])
	}

	// Ensure we're in receiving mode
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	c.debugLog.Printf("Receiving mode: %v, fileInfo: %v", c.receiving, c.fileInfo != nil)
	if !c.receiving || c.fileInfo == nil {
		c.debugLog.Printf("Received data chunk but not in receiving mode")
		return
	}

	// Check if we have enough data for the header (8 bytes)
	if len(data) < 8 {
		c.debugLog.Printf("Received invalid chunk: too short (%d bytes)", len(data))
		return
	}

	// Extract sequence number (first 4 bytes)
	sequence := int(data[0])<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	c.debugLog.Printf("Extracted sequence number: %d", sequence)

	// Extract chunk size (next 4 bytes)
	chunkSize := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
	c.debugLog.Printf("Extracted chunk size: %d", chunkSize)

	// Validate chunk size
	if chunkSize != len(data)-8 {
		c.debugLog.Printf("Chunk size mismatch: header says %d, actual %d", chunkSize, len(data)-8)
		return
	}

	c.debugLog.Printf("Received chunk %d with %d bytes of data", sequence, chunkSize)

	// Append chunk data to our file buffer
	c.fileData = append(c.fileData, data[8:8+chunkSize]...)
	c.debugLog.Printf("Appended chunk data, total received: %d/%d bytes", len(c.fileData), c.fileInfo.Size)

	// Update progress
	progress := float64(len(c.fileData)) / float64(c.fileInfo.Size) * 100
	fmt.Printf("\rReceiving: %.1f%% (%d/%d bytes)", progress, len(c.fileData), c.fileInfo.Size)

	// Send progress update to peer
	if sequence%10 == 0 || sequence == 0 {
		// We'll implement this function later
		// c.sendProgressUpdate(len(c.fileData), sequence)
	}

	// Check if file is complete
	if len(c.fileData) == int(c.fileInfo.Size) {
		c.debugLog.Printf("All chunks received, total size: %d bytes", len(c.fileData))

		// If we've received the file-complete message, verify and save the file
		if c.transferComplete {
			// Release the lock before calling saveReceivedFile
			c.transferMu.Unlock()
			c.saveReceivedFile()
			c.transferMu.Lock() // Re-acquire the lock
		} else {
			c.debugLog.Printf("All chunks received but waiting for file-complete message")
		}
	}
}

// saveReceivedFile saves the received file data to disk
func (c *CLI) saveReceivedFile() {
	c.transferMu.Lock()
	defer c.transferMu.Unlock()

	if !c.receiving || c.fileInfo == nil || len(c.fileData) == 0 {
		c.debugLog.Printf("Cannot save file: no data or file info")
		return
	}

	// Verify file size
	if int64(len(c.fileData)) != c.fileInfo.Size {
		c.debugLog.Printf("File size mismatch: expected %d, got %d", c.fileInfo.Size, len(c.fileData))
		fmt.Printf("\nFile size mismatch: expected %d, got %d bytes\n", c.fileInfo.Size, len(c.fileData))

		// Send verification failure message to sender
		failMsg := map[string]interface{}{
			"type":   "file-failed",
			"reason": fmt.Sprintf("Size mismatch: expected %d, got %d bytes", c.fileInfo.Size, len(c.fileData)),
		}

		if data, err := json.Marshal(failMsg); err == nil {
			c.peer.SendControl(data)
			c.debugLog.Printf("Sent file-failed message to sender")
		} else {
			c.debugLog.Printf("Failed to marshal file-failed message: %v", err)
		}

		return
	}

	// Verify MD5 hash
	hash := md5.Sum(c.fileData)
	md5Hash := fmt.Sprintf("%x", hash)
	if md5Hash != c.fileInfo.MD5 {
		c.debugLog.Printf("MD5 mismatch: expected %s, got %s", c.fileInfo.MD5, md5Hash)
		fmt.Printf("\nMD5 mismatch: file may be corrupted\n")

		// Send verification failure message to sender
		failMsg := map[string]interface{}{
			"type":   "file-failed",
			"reason": fmt.Sprintf("MD5 mismatch: expected %s, got %s", c.fileInfo.MD5, md5Hash),
		}

		if data, err := json.Marshal(failMsg); err == nil {
			c.peer.SendControl(data)
			c.debugLog.Printf("Sent file-failed message to sender")
		} else {
			c.debugLog.Printf("Failed to marshal file-failed message: %v", err)
		}

		return
	}

	// Send verification success message to sender
	verifyMsg := map[string]interface{}{
		"type": "file-verified",
	}

	if data, err := json.Marshal(verifyMsg); err == nil {
		c.peer.SendControl(data)
		c.debugLog.Printf("Sent file-verified message to sender")
	} else {
		c.debugLog.Printf("Failed to marshal file-verified message: %v", err)
	}

	// Save file
	outputPath := c.fileInfo.Name
	// If file exists, add a suffix
	if _, err := os.Stat(outputPath); err == nil {
		ext := filepath.Ext(outputPath)
		base := strings.TrimSuffix(outputPath, ext)
		outputPath = fmt.Sprintf("%s_received%s", base, ext)
	}

	err := os.WriteFile(outputPath, c.fileData, 0644)
	if err != nil {
		c.debugLog.Printf("Failed to save file: %v", err)
		fmt.Printf("\nFailed to save file: %v\n", err)
		return
	}

	fmt.Printf("\nMD5 verification successful!\n")
	fmt.Printf("Expected: %s\n", c.fileInfo.MD5)
	fmt.Printf("Actual:   %s\n", md5Hash)
	fmt.Printf("File saved as: %s\n", outputPath)

	// Reset transfer state
	c.fileInfo = nil
	c.fileData = nil
	c.receiving = false
	c.transferComplete = false
}

// sendCapabilitiesAck sends a capabilities acknowledgment
func (c *CLI) sendCapabilitiesAck(peerMaxChunkSize int) {
	// Use the minimum of our max and peer's max
	ourMaxChunkSize := 16376 // 16384 - 8 bytes for header
	negotiatedSize := ourMaxChunkSize
	if peerMaxChunkSize < negotiatedSize {
		negotiatedSize = peerMaxChunkSize
	}

	ack := map[string]interface{}{
		"type":                "capabilities-ack",
		"negotiatedChunkSize": negotiatedSize,
	}

	data, err := json.Marshal(ack)
	if err != nil {
		c.debugLog.Printf("Error marshaling capabilities-ack: %v", err)
		return
	}

	if err := c.peer.SendControl(data); err != nil {
		c.debugLog.Printf("Error sending capabilities-ack: %v", err)
	}
}

// inputLoop handles user input
func (c *CLI) inputLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")

	for scanner.Scan() {
		text := scanner.Text()
		c.handleInput(text)
		fmt.Print("> ")
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading input: %v\n", err)
	}
}

// handleInput processes user commands
func (c *CLI) handleInput(text string) {
	if text == "" {
		return
	}

	if strings.HasPrefix(text, "/") {
		parts := strings.Fields(text)
		if len(parts) == 0 {
			return
		}

		command := strings.ToLower(parts[0])
		switch command {
		case "/connect":
			if len(parts) != 2 {
				fmt.Println("Usage: /connect <token>")
				return
			}
			token := parts[1]
			c.peerToken = token
			fmt.Printf("Connecting to peer %s...\n", token)

			if c.peer == nil {
				fmt.Println("Not connected to server yet")
				return
			}

			if err := c.peer.Connect(c.wsURL, token); err != nil {
				fmt.Printf("Failed to connect: %v\n", err)
				return
			}

		case "/accept":
			var token string
			if len(parts) == 1 {
				// No token provided, use the last request token
				if c.lastRequestToken == "" {
					fmt.Println("No recent connection requests. Use /accept <token>")
					return
				}
				token = c.lastRequestToken
				fmt.Printf("Using last request token: %s\n", token)
			} else if len(parts) == 2 {
				// Token provided as argument
				token = parts[1]
			} else {
				fmt.Println("Usage: /accept [token]")
				return
			}

			c.peerToken = token
			fmt.Printf("Accepting connection from %s...\n", token)

			if c.peer == nil {
				fmt.Println("Not connected to server yet")
				return
			}

			if err := c.peer.Accept(c.wsURL, token); err != nil {
				fmt.Printf("Failed to accept connection: %v\n", err)
				return
			}

		case "/send":
			if len(parts) != 2 {
				fmt.Println("Usage: /send <filepath>")
				return
			}
			filepath := parts[1]

			if !c.connected {
				fmt.Println("Not connected to a peer")
				return
			}

			if err := c.sendFile(filepath); err != nil {
				fmt.Printf("Failed to send file: %v\n", err)
				return
			}

		case "/link":
			if c.token == "" {
				fmt.Println("Not connected to server yet")
				return
			}
			link := fmt.Sprintf("%s/?token=%s", c.serverURL, c.token)
			fmt.Println("Your connection link:")
			fmt.Println(link)

		case "/quit", "/exit":
			fmt.Println("Exiting...")
			close(c.quit)
			os.Exit(0)

		case "/help":
			fmt.Println("Available commands:")
			fmt.Println("  /connect <token> - Connect to a peer")
			fmt.Println("  /accept [token]  - Accept a connection (uses last request if no token provided)")
			fmt.Println("  /send <filepath> - Send a file")
			fmt.Println("  /link            - Show your connection link")
			fmt.Println("  /quit, /exit     - Exit the application")
			fmt.Println("  /help            - Show this help")

		default:
			fmt.Printf("Unknown command: %s\n", command)
		}
	} else {
		if !c.connected {
			fmt.Println("Not connected to a peer")
			return
		}

		// Use a completely non-blocking approach for sending messages
		// This prevents the UI from hanging if the WebRTC implementation is slow
		fmt.Printf("You: %s\n", text)

		// Send the message in a goroutine to avoid blocking the UI
		go func(message string) {
			if err := c.peer.SendMessage(message); err != nil {
				// Print error on a new line to avoid messing up the prompt
				fmt.Printf("\nFailed to send message: %v\n> ", err)
			}
		}(text)
	}
}

// sendFile sends a file to the connected peer
func (c *CLI) sendFile(filePath string) error {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %v", err)
	}

	// Calculate MD5 hash
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("failed to calculate MD5: %v", err)
	}
	md5Hash := fmt.Sprintf("%x", hash.Sum(nil))

	// Reset file pointer
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to reset file pointer: %v", err)
	}

	// Create file info message
	info := map[string]interface{}{
		"type": "file-info",
		"info": map[string]interface{}{
			"name": filepath.Base(filePath),
			"size": fileInfo.Size(),
			"md5":  md5Hash,
		},
	}

	// Send file info
	infoData, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to marshal file info: %v", err)
	}

	if err := c.peer.SendControl(infoData); err != nil {
		return fmt.Errorf("failed to send file info: %v", err)
	}

	fmt.Printf("Sending file %s (%d bytes)...\n", filepath.Base(filePath), fileInfo.Size())

	// Read file data
	fileData, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	// Start sending chunks
	go c.sendFileChunks(fileData)

	return nil
}

/* Commented out due to duplicate implementation in filechunks.go
// sendFileChunks sends file data in chunks
func (c *CLI) sendFileChunks(fileData []byte) {
	c.debugLog.Printf("Starting to send file chunks, total size: %d bytes", len(fileData))

	// Get the negotiated chunk size from the peer
	var chunkSize int
	if c.peer != nil {
		// Convert int32 to int
		negotiatedSize := int(c.peer.GetNegotiatedChunkSize())

		// Use a smaller chunk size than negotiated to ensure reliable transmission
		// WebRTC has practical limits around 16KB for most implementations
		maxSafeSize := 8192 // 8KB is a very safe limit for WebRTC data channels

		if negotiatedSize > maxSafeSize {
			c.debugLog.Printf("Limiting chunk size to %d bytes for reliability (negotiated was %d)",
				maxSafeSize, negotiatedSize)
			chunkSize = maxSafeSize - 8 // Subtract 8 bytes for header
		} else {
			chunkSize = negotiatedSize - 8 // Subtract 8 bytes for header
		}
	}

	// Fallback to default if we couldn't get the negotiated size
	if chunkSize <= 0 {
		chunkSize = 8192 - 8 // Default to 8KB - 8 bytes for header
	}

	c.debugLog.Printf("Using chunk size of %d bytes (plus 8-byte header)", chunkSize)
	totalChunks := (len(fileData) + chunkSize - 1) / chunkSize

	c.debugLog.Printf("Will send %d chunks of %d bytes each (plus 8-byte header per chunk)", totalChunks, chunkSize)

	// Try to use data channel first, fall back to control channel if needed
	useDataChannel := c.peer != nil && c.peer.IsDataChannelOpen()
	c.debugLog.Printf("Data channel available: %v", useDataChannel)

	for i := 0; i < totalChunks; i++ {
		// Calculate chunk boundaries
		start := i * chunkSize
		end := start + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}

		// Create chunk with header (4 bytes sequence + 4 bytes length)
		chunkData := make([]byte, 8+end-start)

		// Write sequence number (4 bytes)
		chunkData[0] = byte(i >> 24)
		chunkData[1] = byte(i >> 16)
		chunkData[2] = byte(i >> 8)
		chunkData[3] = byte(i)

		// Write chunk size (4 bytes)
		size := end - start
		chunkData[4] = byte(size >> 24)
		chunkData[5] = byte(size >> 16)
		chunkData[6] = byte(size >> 8)
		chunkData[7] = byte(size)

		// Copy chunk data
		copy(chunkData[8:], fileData[start:end])

		c.debugLog.Printf("Sending chunk %d of %d, size: %d bytes (including 8-byte header)", i+1, totalChunks, len(chunkData))

		var err error
		if useDataChannel {
			// Try to send via data channel first
			c.debugLog.Printf("Sending chunk %d via data channel", i)
			err = c.peer.SendData(chunkData)

			// If data channel fails, fall back to control channel
			if err != nil {
				c.debugLog.Printf("Data channel send failed: %v, falling back to control channel", err)
				useDataChannel = false
			}
		}

		// If data channel is not available or failed, use control channel
		if !useDataChannel {
			c.debugLog.Printf("Sending chunk %d via control channel", i)

			// Create a control message with the chunk data
			chunkMsg := map[string]interface{}{
				"type":     "file-chunk",
				"sequence": i,
				"size":     end - start,
				"data":     base64.StdEncoding.EncodeToString(fileData[start:end]),
			}

			// Convert to JSON
			chunkJSON, err := json.Marshal(chunkMsg)
			if err != nil {
				c.debugLog.Printf("Error marshaling chunk %d: %v", i, err)
				continue
			}

			// Send via control channel
			if err := c.peer.SendControl(chunkJSON); err != nil {
				c.debugLog.Printf("Error sending chunk %d: %v", i, err)
				continue
			}

			// Throttle sending to avoid overwhelming the control channel
			// Only add delay when using control channel, and use a shorter delay
			time.Sleep(20 * time.Millisecond)
		}

		// Print progress
		percentage := float64(i+1) / float64(totalChunks) * 100
		fmt.Printf("\rSending: %.1f%% (%d/%d chunks)", percentage, i+1, totalChunks)
	}

	fmt.Println("\nAll chunks sent")

	// Send file complete message
	complete := map[string]interface{}{
		"type": "file-complete",
	}

	completeData, err := json.Marshal(complete)
	if err != nil {
		c.debugLog.Printf("Error marshaling file-complete: %v", err)
		return
	}

	if err := c.peer.SendControl(completeData); err != nil {
		c.debugLog.Printf("Error sending file-complete: %v", err)
	}
}
*/

// dumpGoroutineStacks writes all goroutine stacks to the debug log and a file
func dumpGoroutineStacks(logger *log.Logger) {
	// Get the stack trace
	buf := make([]byte, 1<<20) // 1MB buffer
	stackLen := runtime.Stack(buf, true)
	stack := buf[:stackLen]

	// Log to debug logger
	logger.Printf("=== BEGIN GOROUTINE DUMP ===\n%s\n=== END GOROUTINE DUMP ===", stack)

	// Also write to a file with timestamp
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("goroutine-dump-%s.log", timestamp)

	err := ioutil.WriteFile(filename, stack, 0644)
	if err != nil {
		logger.Printf("Failed to write goroutine dump to file: %v", err)
	} else {
		logger.Printf("Wrote goroutine dump to %s", filename)
		fmt.Printf("Wrote goroutine dump to %s\n", filename)
	}

	// Also write a CPU profile if possible
	cpuProfileName := fmt.Sprintf("cpu-profile-%s.pprof", timestamp)
	f, err := os.Create(cpuProfileName)
	if err != nil {
		logger.Printf("Failed to create CPU profile: %v", err)
	} else {
		logger.Printf("Writing CPU profile to %s", cpuProfileName)
		pprof.StartCPUProfile(f)
		// Stop the profile after 5 seconds
		go func() {
			time.Sleep(5 * time.Second)
			pprof.StopCPUProfile()
			f.Close()
			logger.Printf("CPU profile completed")
		}()
	}
}

func main() {
	flag.Parse()

	// Setup logger
	var err error
	debugLog, err = setupLogger()
	if err != nil {
		fmt.Printf("Error setting up logger: %v\n", err)
		os.Exit(1)
	}

	// Set up signal handler for SIGUSR1 to dump goroutine stacks
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	go func() {
		for sig := range sigChan {
			if sig == syscall.SIGUSR1 {
				fmt.Println("Received SIGUSR1, dumping goroutine stacks...")
				debugLog.Printf("Received SIGUSR1, dumping goroutine stacks...")
				dumpGoroutineStacks(debugLog)
			}
		}
	}()

	debugLog.Printf("Signal handler installed for SIGUSR1 (use 'kill -SIGUSR1 %d' to trigger stack dump)", os.Getpid())
	fmt.Printf("Signal handler installed for SIGUSR1 (use 'kill -SIGUSR1 %d' to trigger stack dump)\n", os.Getpid())

	var serverURLStr string
	var token string

	args := flag.Args()
	if len(args) > 0 {
		var err error
		serverURLStr, token, err = parseConnectionURL(args[0])
		if err != nil {
			fmt.Printf("Error parsing connection URL: %v\n", err)
			os.Exit(1)
		}
	} else {
		serverURLStr = *serverURL // Use the flag's default value
	}

	// Create and start CLI client
	cli := NewCLI(serverURLStr, token, debugLog)
	if err := cli.Start(); err != nil {
		fmt.Printf("Error starting client: %v\n", err)
		os.Exit(1)
	}

	// Wait for quit signal
	<-cli.quit
}
