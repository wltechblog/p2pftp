package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

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

var (
	clients  = make(map[string]*Client)
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins for testing
		},
	}
	mutex = &sync.Mutex{}
)

func main() {
	// Parse command line arguments
	addr := flag.String("addr", "localhost", "Listen address")
	port := flag.Int("port", 8089, "Listen port")
	flag.Parse()

	// Set up WebSocket route
	http.HandleFunc("/ws", handleConnections)

	// Add a simple home page that explains this is just a signaling server
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("P2PFTP Signaling Server\n\nThis server only provides WebSocket signaling for P2PFTP clients.\nThe web interface has been removed to focus on the CLI implementation."))
	})

	// Start the server
	listenAddr := fmt.Sprintf("%s:%d", *addr, *port)
	log.Printf("Signaling server starting on %s", listenAddr)
	log.Printf("WebSocket endpoint: ws://%s/ws", listenAddr)
	err := http.ListenAndServe(listenAddr, nil)
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
