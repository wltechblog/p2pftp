package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Client interface for client operations
type Client interface {
    Connect(peerToken string) error
    Accept(peerToken string) error
    Reject(peerToken string) error
    SendChat(text string) error
    SendFile(path string) error
    Disconnect() error
}

// UI represents the user interface
type UI struct {
    client    Client
    token     string
    running   bool
    mutex     sync.Mutex
    input     *bufio.Reader
    done      chan struct{}
}

// NewUI creates a new UI
func NewUI(client Client) *UI {
    return &UI{
        client:  client,
        input:   bufio.NewReader(os.Stdin),
        running: true,
        done:    make(chan struct{}),
    }
}

// Stop stops the UI
func (ui *UI) Stop() {
    ui.running = false
    close(ui.done)
}

// Run starts the UI
func (ui *UI) Run() error {
    fmt.Println("\nP2PFTP Console Client")
    fmt.Println("Commands: /connect <token>, /accept <token>, /reject <token>, /send <file>, /quit")
    fmt.Println("Type a message and press Enter to chat")
    
    // Start input loop
    for ui.running {
        fmt.Print("> ")
        line, err := ui.input.ReadString('\n')
        if err != nil {
            return err
        }
        
        line = strings.TrimSpace(line)
        if line == "" {
            continue
        }
        
        if strings.HasPrefix(line, "/") {
            ui.handleCommand(line)
        } else {
            if err := ui.client.SendChat(line); err != nil {
                ui.ShowError(fmt.Sprintf("Failed to send chat: %v", err))
            }
        }
    }
    
    return nil
}

// handleCommand handles command input
func (ui *UI) handleCommand(cmd string) {
    parts := strings.Fields(cmd)
    if len(parts) == 0 {
        return
    }
    
    switch parts[0] {
    case "/connect":
        if len(parts) != 2 {
            fmt.Println("Usage: /connect <token>")
            return
        }
        if err := ui.client.Connect(parts[1]); err != nil {
            ui.ShowError(fmt.Sprintf("Connect failed: %v", err))
        }
        
    case "/accept":
        if len(parts) != 2 {
            fmt.Println("Usage: /accept <token>")
            return
        }
        if err := ui.client.Accept(parts[1]); err != nil {
            ui.ShowError(fmt.Sprintf("Accept failed: %v", err))
        }
        
    case "/reject":
        if len(parts) != 2 {
            fmt.Println("Usage: /reject <token>")
            return
        }
        if err := ui.client.Reject(parts[1]); err != nil {
            ui.ShowError(fmt.Sprintf("Reject failed: %v", err))
        }
        
    case "/send":
        if len(parts) != 2 {
            fmt.Println("Usage: /send <file>")
            return
        }
        if err := ui.client.SendFile(parts[1]); err != nil {
            ui.ShowError(fmt.Sprintf("Send failed: %v", err))
        }
        
    case "/quit":
        ui.Stop()
        ui.client.Disconnect()
        
    default:
        fmt.Printf("Unknown command: %s\n", parts[0])
    }
}

// ShowError displays an error message
func (ui *UI) ShowError(msg string) {
    timestamp := time.Now().Format("15:04:05")
    fmt.Printf("\r[%s] [ERROR] %s\n> ", timestamp, msg)
}

// LogDebug logs a debug message
func (ui *UI) LogDebug(msg string) {
    timestamp := time.Now().Format("15:04:05")
    fmt.Printf("\r[%s] %s\n> ", timestamp, msg)
}

// AppendChat appends a chat message
func (ui *UI) AppendChat(msg string) {
    fmt.Printf("\r%s\n> ", msg)
}

// ShowChat displays a chat message
func (ui *UI) ShowChat(from string, msg string) {
    timestamp := time.Now().Format("15:04:05")
    if from == ui.token {
        fmt.Printf("\r[%s] You: %s\n> ", timestamp, msg)
    } else {
        fmt.Printf("\r[%s] Peer: %s\n> ", timestamp, msg)
    }
}

// ShowConnectionRequest shows a connection request
func (ui *UI) ShowConnectionRequest(token string) {
    timestamp := time.Now().Format("15:04:05")
    fmt.Printf("\r[%s] Connection request from %s (use /accept %s or /reject %s)\n> ", 
        timestamp, token, token, token)
}

// ShowConnectionAccepted shows connection accepted
func (ui *UI) ShowConnectionAccepted(msg string) {
    timestamp := time.Now().Format("15:04:05")
    if msg == "" {
        msg = "Connected to Peer"
    }
    fmt.Printf("\r[%s] ✓ %s\n> ", timestamp, msg)
}

// ShowConnectionRejected shows connection rejected
func (ui *UI) ShowConnectionRejected(token string) {
    timestamp := time.Now().Format("15:04:05")
    fmt.Printf("\r[%s] ✗ Connection rejected by %s\n> ", timestamp, token)
}

// SetToken sets the user's token
func (ui *UI) SetToken(token string) {
    ui.mutex.Lock()
    ui.token = token
    ui.mutex.Unlock()
    timestamp := time.Now().Format("15:04:05")
    fmt.Printf("[%s] Your token: %s\n> ", timestamp, token)
}

// UpdateTransferProgress updates transfer progress
func (ui *UI) UpdateTransferProgress(status string, direction string) {
    timestamp := time.Now().Format("15:04:05")
    fmt.Printf("\r[%s] %s\n> ", timestamp, status)
}
