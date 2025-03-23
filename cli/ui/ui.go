package ui

import (
	"fmt"
	"time"
)

type UI struct {
    client        Client
    debugEnabled bool
}

type Client interface {
    Connect(token string) error
    Accept(token string) error
    Disconnect() error
    SendChat(text string) error
    SendFile(path string) error
}

func NewUI(client Client) *UI {
    return &UI{
        client: client,
    }
}

func (u *UI) SetDebug(enabled bool) {
    u.debugEnabled = enabled
}

func (u *UI) ShowError(msg string) {
    fmt.Printf("\n[ERROR] %s\n", msg)
}

func (u *UI) ShowInfo(msg string) {
    fmt.Printf("\n[INFO] %s\n", msg)
}

func (u *UI) LogDebug(msg string) {
    if u.debugEnabled {
        fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), msg)
    }
}

// ShowChat displays a chat message with sender info
func (u *UI) ShowChat(from, msg string) {
    fmt.Printf("\n[%s] %s\n", from, msg)
}

// AppendChat displays a chat message without sender info
func (u *UI) AppendChat(msg string) {
    fmt.Printf("\n%s\n", msg)
}

func (u *UI) ShowConnectionRequest(token string) {
    fmt.Printf("\nConnection request from: %s\nUse /accept %s to accept or /reject %s to reject\n", 
        token, token, token)
}

func (u *UI) ShowConnectionAccepted(msg string) {
    fmt.Printf("\nConnection established.\n")
}

func (u *UI) ShowConnectionRejected(token string) {
    fmt.Printf("\nConnection rejected by peer: %s\n", token)
}

func (u *UI) SetToken(token string) {
    fmt.Printf("\nYour connection token is: %s\nShare this token with peers to allow them to connect.\n", token)
}

func (u *UI) UpdateTransferProgress(status string, direction string) {
    fmt.Printf("\r%s", status)
}
