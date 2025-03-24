package webrtc

import (
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestNewSignaler(t *testing.T) {
	// Create a test WebSocket server
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// Log the headers for inspection
			t.Logf("Request headers: %v", r.Header)
			
			// Verify Origin header is present
			origin := r.Header.Get("Origin")
			if origin != "http://p2pftp-client" {
				t.Errorf("Expected Origin header 'http://p2pftp-client', got '%s'", origin)
				return false
			}
			
			return true
		},
	}

	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if it's a WebSocket upgrade request
		if !websocket.IsWebSocketUpgrade(r) {
			t.Errorf("Expected WebSocket upgrade request")
			http.Error(w, "Not a WebSocket upgrade request", http.StatusBadRequest)
			return
		}

		// Upgrade the connection
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		// Send a test message
		err = conn.WriteJSON(map[string]string{
			"type":  "token",
			"token": "test-token",
		})
		if err != nil {
			t.Errorf("Failed to write message: %v", err)
			return
		}
	}))
	defer server.Close()

	// Convert the server URL to WebSocket URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	
	// Create a debug logger
	debugLog := log.New(os.Stderr, "TEST: ", log.Ltime|log.Lshortfile)

	// Create a new signaler
	signaler, err := NewSignaler(wsURL, "", debugLog)
	if err != nil {
		t.Fatalf("Failed to create signaler: %v", err)
	}
	defer signaler.Close()

	// Verify the signaler was created successfully
	if signaler == nil {
		t.Errorf("Expected signaler to be created")
	}
}
