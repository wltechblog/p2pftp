package main

import (
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/wltechblog/p2pftp/client/transfer"
	"github.com/wltechblog/p2pftp/client/webrtc"
)

var (
debug      = flag.Bool("debug", false, "Enable debug logging")
serverURL  = flag.String("server", "https://p2p.teamworkapps.com", "Signaling server URL")
connectURL = flag.String("url", "", "Full connection URL (e.g., https://p2p.teamworkapps.com/?token=abcd1234)")
debugLog   *log.Logger
)

// parseConnectionURL extracts the server URL and token from a connection URL
func parseConnectionURL(urlStr string) (string, string, error) {
if urlStr == "" {
return "", "", nil
}

parsed, err := url.Parse(urlStr)
if err != nil {
return "", "", fmt.Errorf("invalid URL: %v", err)
}

token := parsed.Query().Get("token")
if token == "" {
return "", "", fmt.Errorf("URL missing token parameter")
}

// Remove query parameters to get base server URL
parsed.RawQuery = ""
return parsed.String(), token, nil
}

// getWebSocketURL converts HTTP/HTTPS URL to WS/WSS URL
func getWebSocketURL(httpURL string) string {
if strings.HasPrefix(httpURL, "https://") {
return "wss://" + strings.TrimPrefix(httpURL, "https://") + "/signal"
} else if strings.HasPrefix(httpURL, "http://") {
return "ws://" + strings.TrimPrefix(httpURL, "http://") + "/signal"
}
return httpURL
}

type Client struct {
app        *tview.Application
chatView   *tview.TextView
inputField *tview.InputField
debugView  *tview.TextView
peer       *webrtc.Peer
transfer   *transfer.Transfer
wsURL      string
serverURL  string
token      string
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

// Connect to server and get token
peer, err := webrtc.NewPeer(debugLog)
if err != nil {
c.logChat("[red]Failed to create peer: %v[-]", err)
return
}
peer.SetTokenHandler(func(token string) {
c.token = token
c.logChat("[green]Your token: %s[-]", token)
c.logChat("[green]Your connection link: %s/?token=%s[-]", c.serverURL, token)
})
c.peer = peer

if err := peer.Connect(c.wsURL, ""); err != nil {
c.logChat("[red]Failed to connect to server: %v[-]", err)
return
}
}

func (c *Client) handleInput(text string) {
if text == "" {
return
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

if c.peer == nil {
c.logChat("[red]Not connected to server yet[-]")
return
}

peer := c.peer
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

if c.peer == nil {
c.logChat("[red]Not connected to server yet[-]")
return
}

peer := c.peer
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
c.logChat("[red]Not connected to server yet[-]")
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

case "/link":
if c.token == "" {
c.logChat("[red]Not connected to server yet[-]")
return
}
link := fmt.Sprintf("%s/?token=%s", c.serverURL, c.token)
c.logChat("[green]Your connection link:[-]")
c.logChat("%s", link)

case "/help":
c.logChat("[green]Available commands:[-]")
c.logChat("  /connect <token> - Connect to a peer")
c.logChat("  /accept <token>  - Accept a connection")
c.logChat("  /send <filepath> - Send a file")
c.logChat("  /link           - Show your connection link")
c.logChat("  /help           - Show this help")

default:
c.logChat("[red]Unknown command: %s[-]", command)
}
} else {
// Handle chat messages
if c.peer == nil {
c.logChat("[red]Not connected to server yet[-]")
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

// Parse connection URL if provided
serverURL := *serverURL
var token string
if *connectURL != "" {
var err error
serverURL, token, err = parseConnectionURL(*connectURL)
if err != nil {
fmt.Printf("Error parsing connection URL: %v\n", err)
os.Exit(1)
}
}

// Create and setup client
client := NewClient()
client.serverURL = serverURL
client.wsURL = getWebSocketURL(serverURL)
if token != "" {
client.token = token
}
client.setupUI()

// Show initial help message
client.logChat("[green]Welcome to P2P Chat![-]")
client.logChat("[green]Server: %s[-]", client.serverURL)
client.logChat("[green]Type /help for available commands[-]")

// Start the application
if err := client.app.Run(); err != nil {
fmt.Printf("Error running application: %v\n", err)
os.Exit(1)
}
}
