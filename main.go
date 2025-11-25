package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

//go:embed web/static
var staticFiles embed.FS

// Client represents a connected user
type Client struct {
	conn      *websocket.Conn
	token     string
	peerToken string
}

// Message represents the WebSocket message structure
type Message struct {
	Type      string `json:"type"`
	Token     string `json:"token,omitempty"`
	PeerToken string `json:"peerToken,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	ICE       string `json:"ice,omitempty"`
}

// ConfigResponse represents the configuration returned to clients
type ConfigResponse struct {
	StunServers []string `json:"stunServers"`
}

var (
	clients  = make(map[string]*Client)
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for testing
		},
	}
	mutex      = &sync.Mutex{}
	stunServers []string
)

func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ConfigResponse{
		StunServers: stunServers,
	})
}

func main() {
	// Parse command line arguments
	addr := flag.String("addr", "localhost", "Listen address")
	port := flag.Int("port", 8089, "Listen port")
	stunFlag := flag.String("stun", "", "Comma-separated list of STUN servers (default: Google STUN servers)")
	flag.Parse()

	// Set STUN servers
	if *stunFlag != "" {
		stunServers = strings.Split(*stunFlag, ",")
		for i, server := range stunServers {
			stunServers[i] = strings.TrimSpace(server)
		}
	} else {
		// Default STUN servers
		stunServers = []string{
			"stun:stun.l.google.com:19302",
			"stun:stun1.l.google.com:19302",
			"stun:stun2.l.google.com:19302",
			"stun:stun3.l.google.com:19302",
			"stun:stun4.l.google.com:19302",
		}
	}

	// Set up config endpoint
	http.HandleFunc("/api/config", handleConfig)

	// Set up WebSocket route
	http.HandleFunc("/ws", handleConnections)

	// Set up static file server for web client
	staticFS, err := fs.Sub(staticFiles, "web/static")
	if err != nil {
		log.Fatal("Failed to create sub filesystem:", err)
	}

	// Handle root path explicitly to avoid redirect loops
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			// Serve index.html directly for the root path
			content, err := fs.ReadFile(staticFS, "index.html")
			if err != nil {
				http.Error(w, "Could not read index.html", http.StatusInternalServerError)
				log.Printf("Error reading index.html: %v", err)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(content)
			return
		}

		// For all other paths, strip the leading slash and serve the file
		path := r.URL.Path
		if len(path) > 0 && path[0] == '/' {
			path = path[1:]
		}

		content, err := fs.ReadFile(staticFS, path)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Set content type based on file extension
		contentType := http.DetectContentType(content)
		if path[len(path)-4:] == ".css" {
			contentType = "text/css; charset=utf-8"
		} else if path[len(path)-3:] == ".js" {
			contentType = "application/javascript; charset=utf-8"
		}

		w.Header().Set("Content-Type", contentType)
		w.Write(content)
	})

	// Start the server
	listenAddr := fmt.Sprintf("%s:%d", *addr, *port)
	log.Printf("P2PFTP Server starting on %s", listenAddr)
	log.Printf("Web interface: http://%s/", listenAddr)
	log.Printf("WebSocket endpoint: ws://%s/ws", listenAddr)

	err = http.ListenAndServe(listenAddr, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	log.Printf("Starting websocket for %s", r.Header.Get("X-Forwarded-For"))
	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Error upgrading to WebSocket:", err)
		return
	}
	defer conn.Close()

	// Generate a token for this client
	token := generateToken()
	client := &Client{
		conn:  conn,
		token: token,
	}

	// Register the client
	mutex.Lock()
	clients[token] = client
	mutex.Unlock()

	// Send the token to the client
	if err := conn.WriteJSON(Message{
		Type:  "token",
		Token: token,
	}); err != nil {
		log.Println("Error sending token:", err)
		return
	}

	// Handle WebSocket messages
	for {
		var msg Message
		err := conn.ReadJSON(&msg)
		if err != nil {
			log.Println("Error reading message:", err)
			break
		}

		switch msg.Type {
		case "connect":
			handleConnect(client, msg.PeerToken)
		case "accept":
			handleAccept(client, msg.PeerToken)
		case "reject":
			handleReject(client, msg.PeerToken)
		case "ice":
			forwardICE(client, msg)
		case "offer":
			forwardOffer(client, msg)
		case "answer":
			forwardAnswer(client, msg)
		}
	}

	// Unregister client when disconnected
	mutex.Lock()
	delete(clients, client.token)
	mutex.Unlock()
}

func generateToken() string {
	return uuid.New().String()[:8]
}

func handleConnect(client *Client, peerToken string) {
	// Find the peer client
	mutex.Lock()
	peerClient, exists := clients[peerToken]
	mutex.Unlock()

	if !exists {
		// Peer not found
		client.conn.WriteJSON(Message{
			Type: "error",
			SDP:  "Peer not found",
		})
		return
	}

	// Store the peer token
	client.peerToken = peerToken

	// Notify the peer about the connection request
	peerClient.conn.WriteJSON(Message{
		Type:  "request",
		Token: client.token,
	})
}

func handleAccept(client *Client, peerToken string) {
	mutex.Lock()
	peerClient, exists := clients[peerToken]
	mutex.Unlock()

	if !exists {
		client.conn.WriteJSON(Message{
			Type: "error",
			SDP:  "Peer not found",
		})
		return
	}

	// Notify the original client that the connection was accepted
	peerClient.conn.WriteJSON(Message{
		Type:  "accepted",
		Token: client.token,
	})
}

func handleReject(client *Client, peerToken string) {
	mutex.Lock()
	peerClient, exists := clients[peerToken]
	mutex.Unlock()

	if !exists {
		return
	}

	// Notify the original client that the connection was rejected
	peerClient.conn.WriteJSON(Message{
		Type:  "rejected",
		Token: client.token,
	})
}

func forwardOffer(client *Client, msg Message) {
	mutex.Lock()
	peerClient, exists := clients[msg.PeerToken]
	mutex.Unlock()

	if !exists {
		client.conn.WriteJSON(Message{
			Type: "error",
			SDP:  "Peer not found",
		})
		return
	}

	// Forward the offer to the peer
	peerClient.conn.WriteJSON(Message{
		Type:  "offer",
		Token: client.token,
		SDP:   msg.SDP,
	})
}

func forwardAnswer(client *Client, msg Message) {
	mutex.Lock()
	peerClient, exists := clients[msg.PeerToken]
	mutex.Unlock()

	if !exists {
		client.conn.WriteJSON(Message{
			Type: "error",
			SDP:  "Peer not found",
		})
		return
	}

	// Forward the answer to the peer
	peerClient.conn.WriteJSON(Message{
		Type:  "answer",
		Token: client.token,
		SDP:   msg.SDP,
	})
}

func forwardICE(client *Client, msg Message) {
	mutex.Lock()
	peerClient, exists := clients[msg.PeerToken]
	mutex.Unlock()

	if !exists {
		client.conn.WriteJSON(Message{
			Type: "error",
			SDP:  "Peer not found",
		})
		return
	}

	// Forward the ICE candidate to the peer
	peerClient.conn.WriteJSON(Message{
		Type:  "ice",
		Token: client.token,
		ICE:   msg.ICE,
	})
}
