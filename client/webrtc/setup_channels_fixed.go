package webrtc

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v3"
)

// setupControlChannel sets up the control channel handlers
func (p *Peer) setupControlChannel(dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		p.debugLog.Printf("Control channel opened")
		// Send capabilities after channel is open
		p.sendCapabilities()
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// Log message details with more information for debugging
		if msg.IsString {
			p.debugLog.Printf("Control message received (string): %s", string(msg.Data))
		} else {
			p.debugLog.Printf("Control message received (binary): %d bytes", len(msg.Data))
			// Print first few bytes for debugging
			if len(msg.Data) > 0 {
				maxBytes := 32
				if len(msg.Data) < maxBytes {
					maxBytes = len(msg.Data)
				}
				p.debugLog.Printf("First %d bytes: %v", maxBytes, msg.Data[:maxBytes])
			}
		}

		// Always try to parse as JSON regardless of IsString flag
		var msgData map[string]interface{}
		if err := json.Unmarshal(msg.Data, &msgData); err != nil {
			p.debugLog.Printf("Error parsing control message: %v", err)
			// Still pass the raw data to the control handler
			if p.controlHandler != nil {
				p.controlHandler(msg.Data)
			}
			return
		}

		// Successfully parsed JSON, now handle by message type
		msgType, ok := msgData["type"].(string)
		if !ok {
			p.debugLog.Printf("Message missing 'type' field: %v", msgData)
			return
		}

		p.debugLog.Printf("Received message of type: %s", msgType)

		// Handle different message types
		switch msgType {
		case "message":
			// Handle chat message
			content, ok := msgData["content"].(string)
			if ok && p.messageHandler != nil {
				p.debugLog.Printf("Dispatching chat message: %s", content)
				p.messageHandler(content)
			} else {
				p.debugLog.Printf("Invalid message format or missing content field: %v", msgData)
			}
		case "capabilities":
			// Handle capabilities message directly
			p.handleCapabilities(msg.Data)
		case "capabilities-ack":
			// Handle capabilities acknowledgment directly
			p.handleCapabilitiesAck(msg.Data)
		default:
			// Pass to general control handler
			if p.controlHandler != nil {
				p.controlHandler(msg.Data)
			}
		}
	})

	dc.OnClose(func() {
		p.debugLog.Printf("Control channel closed")
	})

	dc.OnError(func(err error) {
		p.debugLog.Printf("Control channel error: %v", err)
		if p.errorHandler != nil {
			p.errorHandler(fmt.Sprintf("Control channel error: %v", err))
		}
	})
}

// setupDataChannel sets up the data channel handlers
func (p *Peer) setupDataChannel(dc *webrtc.DataChannel) {
	p.debugLog.Printf("Setting up data channel (ID: %d, Label: %s)", dc.ID(), dc.Label())

	// Set buffer thresholds for better performance
	dc.SetBufferedAmountLowThreshold(65536) // 64KB

	dc.OnBufferedAmountLow(func() {
		p.debugLog.Printf("Data channel buffer low event (ID: %d)", dc.ID())
	})

	dc.OnOpen(func() {
		p.debugLog.Printf("*** DATA CHANNEL OPENED (ID: %d, Label: %s) ***", dc.ID(), dc.Label())
		p.debugLog.Printf("Data channel state: %s", dc.ReadyState().String())
		p.debugLog.Printf("Data channel buffered amount: %d", dc.BufferedAmount())

		// Notify status handler about the data channel being open
		if p.statusHandler != nil {
			p.statusHandler(fmt.Sprintf("Data channel opened (ID: %d, Label: %s)", dc.ID(), dc.Label()))
		}

		// Send a test message to verify the channel is working
		testData := []byte{0, 0, 0, 0, 0, 0, 0, 8, 1, 2, 3, 4, 5, 6, 7, 8}
		p.debugLog.Printf("Sending test message on data channel open")
		err := dc.Send(testData)
		if err != nil {
			p.debugLog.Printf("Failed to send test message: %v", err)
		} else {
			p.debugLog.Printf("Test message sent successfully")
		}
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		// For data channel, we expect binary data
		if msg.IsString {
			p.debugLog.Printf("Warning: Received string data on binary channel (ID: %d): %s", dc.ID(), string(msg.Data))
		} else {
			p.debugLog.Printf("Data channel message received (ID: %d): %d bytes", dc.ID(), len(msg.Data))
			// Print first few bytes for debugging
			if len(msg.Data) > 0 {
				maxBytes := 16
				if len(msg.Data) < maxBytes {
					maxBytes = len(msg.Data)
				}
				p.debugLog.Printf("First %d bytes: %v", maxBytes, msg.Data[:maxBytes])

				// If data starts with a sequence number, extract and log it
				if len(msg.Data) >= 8 {
					sequence := int(msg.Data[0])<<24 | int(msg.Data[1])<<16 | int(msg.Data[2])<<8 | int(msg.Data[3])
					chunkSize := int(msg.Data[4])<<24 | int(msg.Data[5])<<16 | int(msg.Data[6])<<8 | int(msg.Data[7])
					p.debugLog.Printf("Data appears to be chunk %d with size %d", sequence, chunkSize)
				}
			}
		}

		// Pass data to handler regardless of type
		if p.dataHandler != nil {
			p.debugLog.Printf("Calling data handler with %d bytes", len(msg.Data))
			p.dataHandler(msg.Data)
		} else {
			p.debugLog.Printf("No data handler registered to process binary data")
		}
	})

	dc.OnClose(func() {
		p.debugLog.Printf("Data channel closed (ID: %d)", dc.ID())
	})

	dc.OnError(func(err error) {
		p.debugLog.Printf("Data channel error (ID: %d): %v", dc.ID(), err)
		if p.errorHandler != nil {
			p.errorHandler(fmt.Sprintf("Data channel error: %v", err))
		}
	})
}
