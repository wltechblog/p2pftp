package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type UI struct {
client *Client
token  string
lastRequest string // Track the most recent request token
}

func NewUI(client *Client) *UI {
return &UI{
client: client,
}
}

func (ui *UI) printPrompt() {
fmt.Print("> ")
}

func (ui *UI) Run() error {
reader := bufio.NewReader(os.Stdin)
fmt.Println("P2P FTP Client")
fmt.Println("Commands:")
fmt.Println("  /token - Show your token")
fmt.Println("  /connect <token> - Connect to a peer")
fmt.Println("  /accept [token] - Accept connection request (defaults to most recent)")
fmt.Println("  /reject [token] - Reject connection request (defaults to most recent)")
fmt.Println("  /quit - Exit program")
fmt.Println()

ui.printPrompt()

for {
line, err := reader.ReadString('\n')
if err != nil {
return err
}

line = strings.TrimSpace(line)
if line == "" {
ui.printPrompt()
continue
}

parts := strings.Fields(line)
cmd := parts[0]

switch cmd {
case "/quit":
return nil

case "/token":
if ui.token == "" {
fmt.Println("Token not yet received. Please wait...")
} else {
fmt.Printf("Your token: %s\n", ui.token)
}

case "/connect":
if len(parts) != 2 {
fmt.Println("Usage: /connect <token>")
} else if ui.token == "" {
fmt.Println("Please wait for your token before connecting")
} else {
if err := ui.client.Connect(parts[1]); err != nil {
fmt.Printf("Error connecting: %v\n", err)
}
}

case "/accept":
if ui.token == "" {
fmt.Println("Please wait for your token before accepting")
} else {
var tokenToAccept string
if len(parts) > 1 {
tokenToAccept = parts[1]
} else if ui.lastRequest != "" {
tokenToAccept = ui.lastRequest
} else {
fmt.Println("No pending request to accept")
break
}

if err := ui.client.Accept(tokenToAccept); err != nil {
fmt.Printf("Error accepting: %v\n", err)
} else {
ui.lastRequest = "" // Clear the last request after accepting
}
}

case "/reject":
if ui.token == "" {
fmt.Println("Please wait for your token before rejecting")
} else {
var tokenToReject string
if len(parts) > 1 {
tokenToReject = parts[1]
} else if ui.lastRequest != "" {
tokenToReject = ui.lastRequest
} else {
fmt.Println("No pending request to reject")
break
}

if err := ui.client.Reject(tokenToReject); err != nil {
fmt.Printf("Error rejecting: %v\n", err)
} else {
ui.lastRequest = "" // Clear the last request after rejecting
}
}

default:
fmt.Printf("Unknown command: %s\n", cmd)
}

ui.printPrompt()
}
}

func (ui *UI) SetToken(token string) {
ui.token = token
fmt.Printf("\rToken received: %s\n", token)
ui.printPrompt()
}

func (ui *UI) ShowConnectionRequest(token string) {
ui.lastRequest = token // Store the request token
fmt.Printf("\rConnection request from: %s (use /accept to accept)\n", token)
ui.printPrompt()
}

func (ui *UI) ShowConnectionAccepted(msg string) {
fmt.Printf("\r%s\n", msg)
ui.printPrompt()
}

func (ui *UI) ShowConnectionRejected(token string) {
fmt.Printf("\rConnection rejected by %s\n", token)
ui.printPrompt()
}

func (ui *UI) ShowError(msg string) {
fmt.Printf("\rError: %s\n", msg)
ui.printPrompt()
}

func (ui *UI) LogDebug(msg string) {
fmt.Printf("\r[DEBUG] %s\n", msg)
ui.printPrompt()
}
