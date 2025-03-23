package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/wltechblog/p2pftp/client/transfer"
	"github.com/wltechblog/p2pftp/client/webrtc"
)

var (
	debug    = flag.Bool("debug", false, "Enable debug logging")
	debugLog *log.Logger
)

type Client struct {
app        *tview.Application
chatView   *tview.TextView
inputField *tview.InputField
debugView  *tview.TextView
peer       *webrtc.Peer
transfer   *transfer.Transfer
wsURL      string
}

func NewClient() *Client {
	app := tview.NewApplication()
	chatView := tview.NewTextView().
		SetDynamicColors(true).
		SetChangedFunc(func() {
			app.Draw()
		})
	chatView.SetTitle("Chat").SetBorder(true)

	debugView := tview.NewTextView().
		SetDynamicColors(true).
		SetChangedFunc(func() {
			app.Draw()
		})
	debugView.SetTitle("Debug Log").SetBorder(true)

	inputField := tview.NewInputField().
		SetLabel("> ").
		SetFieldWidth(0)

	return &Client{
		app:        app,
		chatView:   chatView,
		inputField: inputField,
		debugView:  debugView,
	}
}

func (c *Client) setupUI() {
	// Create a flex layout
	flex := tview.NewFlex().SetDirection(tview.FlexRow)

	// Add chat view taking most of the space
	flex.AddItem(c.chatView, 0, 3, false)

	// Add debug view if debug mode is enabled
	if *debug {
		flex.AddItem(c.debugView, 0, 1, false)
	}

	// Add input field at the bottom
	flex.AddItem(c.inputField, 1, 0, true)

	// Handle input
	c.inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			text := c.inputField.GetText()
			c.handleInput(text)
			c.inputField.SetText("")
		}
	})

	// Set the root and focus
	c.app.SetRoot(flex, true)
	c.app.SetFocus(c.inputField)
}

func (c *Client) handleInput(text string) {
if text == "" {
return
}

if c.wsURL == "" {
c.wsURL = "wss://localhost:8080/signal"
}

// Format timestamp
timestamp := time.Now().Format("15:04:05")

if strings.HasPrefix(text, "/") {
		// Handle commands
		parts := strings.Fields(text)
		if len(parts) == 0 {
			return
		}

		command := strings.ToLower(parts[0])
		switch command {
case "/connect":
if len(parts) != 2 {
c.logChat("[red]Usage: /connect <token>[-]")
return
}
token := parts[1]
c.logDebug("Connecting to peer with token: %s", token)
c.logChat("[yellow]Connecting to peer %s...", token)

if c.peer != nil {
c.logChat("[red]Already connected to a peer[-]")
return
}

peer, err := webrtc.NewPeer(debugLog)
if err != nil {
c.logChat("[red]Failed to create peer: %v[-]", err)
return
}
c.peer = peer

peer.SetMessageHandler(func(msg string) {
c.logChat("[blue]<%s> %s[-]", token, msg)
})

peer.SetStatusHandler(func(status string) {
c.logChat("[yellow]%s[-]", status)
})

peer.SetControlHandler(func(data []byte) {
// TODO: Handle control messages (file transfer)
c.logDebug("Received control message: %s", string(data))
})

if err := peer.Connect(c.wsURL, token); err != nil {
c.logChat("[red]Failed to connect: %v[-]", err)
return
}
		
case "/accept":
if len(parts) != 2 {
c.logChat("[red]Usage: /accept <token>[-]")
return
}
token := parts[1]
c.logDebug("Accepting connection from peer with token: %s", token)
c.logChat("[yellow]Accepting connection from %s...", token)

if c.peer != nil {
c.logChat("[red]Already connected to a peer[-]")
return
}

peer, err := webrtc.NewPeer(debugLog)
if err != nil {
c.logChat("[red]Failed to create peer: %v[-]", err)
return
}
c.peer = peer

peer.SetMessageHandler(func(msg string) {
c.logChat("[blue]<%s> %s[-]", token, msg)
})

peer.SetStatusHandler(func(status string) {
c.logChat("[yellow]%s[-]", status)
})

peer.SetControlHandler(func(data []byte) {
// TODO: Handle control messages (file transfer)
c.logDebug("Received control message: %s", string(data))
})

if err := peer.Accept(c.wsURL, token); err != nil {
c.logChat("[red]Failed to accept connection: %v[-]", err)
return
}

case "/send":
if len(parts) != 2 {
c.logChat("[red]Usage: /send <filepath>[-]")
return
}
filepath := parts[1]
c.logDebug("Sending file: %s", filepath)

if c.peer == nil {
c.logChat("[red]Not connected to a peer[-]")
return
}

t, err := transfer.NewSender(filepath, 0, debugLog)
if err != nil {
c.logChat("[red]Failed to prepare file: %v[-]", err)
return
}
c.transfer = t

fileInfo := t.Info()
c.logChat("[yellow]Sending file %s (%d bytes)...[-]", fileInfo.Name, fileInfo.Size)
if err := t.Start(); err != nil {
c.logChat("[red]Failed to start transfer: %v[-]", err)
return
}

		case "/help":
			c.logChat("[green]Available commands:[-]")
			c.logChat("  /connect <token> - Connect to a peer")
			c.logChat("  /accept <token>  - Accept a connection")
			c.logChat("  /send <filepath> - Send a file")
			c.logChat("  /help           - Show this help")

		default:
			c.logChat("[red]Unknown command: %s[-]", command)
		}
} else {
// Handle chat messages
if c.peer == nil {
c.logChat("[red]Not connected to a peer[-]")
return
}

if err := c.peer.SendMessage(text); err != nil {
c.logChat("[red]Failed to send message: %v[-]", err)
return
}
c.logChat("[green]<%s> %s[-]", timestamp, text)
	}
}

func (c *Client) logChat(format string, args ...interface{}) {
	fmt.Fprintf(c.chatView, format+"\n", args...)
}

func (c *Client) logDebug(format string, args ...interface{}) {
	if *debug {
		fmt.Fprintf(c.debugView, "[gray]%s %s[-]\n",
			time.Now().Format("15:04:05"),
			fmt.Sprintf(format, args...))
	}
}

func main() {
	// Parse command line flags
	flag.Parse()

	// Set up debug logging
	if *debug {
		debugLog = log.New(os.Stderr, "DEBUG: ", log.Ltime|log.Lshortfile)
	}

	// Create and setup client
	client := NewClient()
	client.setupUI()

	// Show initial help message
	client.logChat("[green]Welcome to P2P Chat![-]")
	client.logChat("[green]Type /help for available commands[-]")

	// Start the application
	if err := client.app.Run(); err != nil {
		fmt.Printf("Error running application: %v\n", err)
		os.Exit(1)
	}
}
