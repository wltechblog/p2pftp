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
fmt.Println("  /connect <token> - Connect to a peer")
fmt.Println("  /accept <token> - Accept connection request")
fmt.Println("  /reject <token> - Reject connection request")
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

case "/connect":
if len(parts) != 2 {
fmt.Println("Usage: /connect <token>")
} else {
if err := ui.client.Connect(parts[1]); err != nil {
fmt.Printf("Error connecting: %v\n", err)
}
}

case "/accept":
if len(parts) != 2 {
fmt.Println("Usage: /accept <token>")
} else {
if err := ui.client.Accept(parts[1]); err != nil {
fmt.Printf("Error accepting: %v\n", err)
}
}

case "/reject":
if len(parts) != 2 {
fmt.Println("Usage: /reject <token>")
} else {
if err := ui.client.Reject(parts[1]); err != nil {
fmt.Printf("Error rejecting: %v\n", err)
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
fmt.Printf("\rYour token: %s\n", token)
ui.printPrompt()
}

func (ui *UI) ShowConnectionRequest(token string) {
fmt.Printf("\rConnection request from: %s\n", token)
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
