package main

import (
	"flag"
	"fmt"
	"io"
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
	logFile    = flag.String("logfile", "p2pftp-debug.log", "Path to debug log file")
	serverURL  = flag.String("server", "p2p.teamworkapps.com", "Signaling server hostname (port 443 will be used)")
	connectURL = flag.String("url", "", "Full connection URL (e.g., https://p2p.teamworkapps.com/?token=abcd1234)")
	debugLog   *log.Logger
)

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

 // Parse and ensure path
 u, err := url.Parse(httpURL)
 if err != nil {
  logDebug("Error parsing URL: %v", err)
  return ""
 }
 
 // Only set path if it's empty or root
 if u.Path == "" || u.Path == "/" {
  u.Path = "/signal"
  logDebug("Set default path: %s", u.Path)
 }
 
 result := u.String()
 logDebug("Final WebSocket URL: %s", result)
 return result
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
debugLog   *log.Logger
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
debugLog:   debugLog,
}
}

func (c *Client) setupUI() {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)

	flex.AddItem(c.chatView, 0, 3, false)

if *debug {
 c.debugLog.SetPrefix("DEBUG: ")
 c.debugLog.SetFlags(log.LstdFlags)
}

	flex.AddItem(c.inputField, 1, 0, true)

	c.inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			text := c.inputField.GetText()
			c.handleInput(text)
			c.inputField.SetText("")
		}
	})

	c.app.SetRoot(flex, true)
	c.app.SetFocus(c.inputField)

peer, err := webrtc.NewPeer(c.debugLog)
if err != nil {
c.logChat("[red]Failed to create peer: %v[-]", err)
return
}
peer.SetTokenHandler(func(token string) {
c.token = token
c.logChat("[green]Your token: %s[-]", token)
link := fmt.Sprintf("%s/?token=%s", c.serverURL, c.token)
c.logChat("[green]Your connection link: %s[-]", link)
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

	timestamp := time.Now().Format("15:04:05")

	if strings.HasPrefix(text, "/") {
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
	flag.Parse()

	// Setup logger
	var err error
	debugLog, err = setupLogger()
	if err != nil {
		fmt.Printf("Error setting up logger: %v\n", err)
		os.Exit(1)
	}

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

	client := NewClient()
	client.serverURL = serverURLStr
	client.wsURL = getWebSocketURL(serverURLStr)
	if token != "" {
		client.token = token
	}
	client.setupUI()

	client.logChat("[green]Welcome to P2P Chat![-]")
	client.logChat("[green]Server: %s[-]", client.serverURL)
	client.logChat("[green]Type /help for available commands[-]")

	if err := client.app.Run(); err != nil {
		fmt.Printf("Error running application: %v\n", err)
		os.Exit(1)
	}
}
