package webrtc

import (
	"encoding/json"
	"fmt"

	pionwebrtc "github.com/pion/webrtc/v3"
)

// Channels handles WebRTC data channels
type Channels struct {
	connection  *Connection
	logger      Logger
	msgHandler  MessageHandler
	dataHandler DataHandler
}

// MessageHandler is an interface for handling control messages
type MessageHandler interface {
	HandleControlMessage(msg []byte) error
}

// DataHandler is an interface for handling data chunks
type DataHandler interface {
	HandleDataChunk(data []byte) error
}

// NewChannels creates a new channels instance
func NewChannels(
	connection *Connection,
	logger Logger,
	msgHandler MessageHandler,
	dataHandler DataHandler,
) *Channels {
	return &Channels{
		connection:  connection,
		logger:      logger,
		msgHandler:  msgHandler,
		dataHandler: dataHandler,
	}
}

// SetupChannelHandlers sets up handlers for the data channels
func (c *Channels) SetupChannelHandlers() {
	c.logger.LogDebug("Setting up channel handlers")

	// Set up control channel handler
	if c.connection.GetControlChannel() != nil {
		c.logger.LogDebug(fmt.Sprintf("Control channel state: %s", c.connection.GetControlChannel().ReadyState().String()))
		
		c.connection.GetControlChannel().OnMessage(func(msg pionwebrtc.DataChannelMessage) {
			// Log the message receipt immediately
// Process the message in a separate goroutine to avoid blocking the WebRTC thread
go func() {
c.logger.LogDebug("====== WEBRTC MESSAGE RECEIVED START ======")
c.logger.LogDebug(fmt.Sprintf("Raw message data: %s", string(msg.Data)))
c.logger.LogDebug(fmt.Sprintf("Message is binary: %v", !msg.IsString))
				
			// Process the message in a separate goroutine to avoid blocking the WebRTC thread
			msgData := msg.Data // Make a copy of the data
			go func() {
				c.logger.LogDebug(fmt.Sprintf("Processing message in goroutine: %s", string(msgData)))
				c.logger.LogDebug(fmt.Sprintf("Message binary: %v", msg.IsString == false))
					c.logger.LogDebug(fmt.Sprintf("Message length: %d", len(msgData)))
					
// Try to parse the message to see if it's valid JSON
var parsed map[string]interface{}
if err := json.Unmarshal(msgData, &parsed); err != nil {
    c.logger.LogDebug(fmt.Sprintf("ERROR: Failed to parse JSON: %v", err))
    c.logger.LogDebug(fmt.Sprintf("Raw message that failed to parse: %s", string(msgData)))
} else {
    c.logger.LogDebug(fmt.Sprintf("Successfully parsed JSON message: %+v", parsed))
if msgType, ok := parsed["type"].(string); ok {
c.logger.LogDebug(fmt.Sprintf("Message type: %s", msgType))
if msgType == "message" {
    if content, ok := parsed["content"].(string); ok {
        c.logger.AppendChat(content)
    }
}
}
					}
					
				if c.msgHandler != nil {
						c.logger.LogDebug("Calling HandleControlMessage from goroutine")
						err := c.msgHandler.HandleControlMessage(msgData)
						if err != nil {
							c.logger.LogDebug(fmt.Sprintf("Error handling control message: %v", err))
						} else {
							c.logger.LogDebug("HandleControlMessage completed successfully")
						}
					} else {
						c.logger.LogDebug("ERROR: msgHandler is nil, cannot handle control message")
					}
			}()
			}()
		})
		
		// Add buffer threshold callback
		c.connection.GetControlChannel().SetBufferedAmountLowThreshold(65536) // 64KB
		c.connection.GetControlChannel().OnBufferedAmountLow(func() {
			c.logger.LogDebug("Control channel buffer amount low event triggered")
		})
	} else {
		c.logger.LogDebug("Control channel is nil, cannot set up handler")
	}

	// Set up data channel handler
	if c.connection.GetDataChannel() != nil {
		c.logger.LogDebug(fmt.Sprintf("Data channel state: %s", c.connection.GetDataChannel().ReadyState().String()))
		
		c.connection.GetDataChannel().OnMessage(func(msg pionwebrtc.DataChannelMessage) {
			// Log the message receipt immediately
			// Process the message in a separate goroutine to avoid blocking the WebRTC thread
			go func() {
				c.logger.LogDebug(fmt.Sprintf("Received data chunk: %d bytes (will process in goroutine)", len(msg.Data)))
			
			// Process the message in a separate goroutine to avoid blocking the WebRTC thread
			msgData := msg.Data // Make a copy of the data
			go func() {
				c.logger.LogDebug(fmt.Sprintf("Processing data chunk in goroutine: %d bytes", len(msgData)))
				
					if c.dataHandler != nil {
							c.logger.LogDebug("Calling HandleDataChunk from goroutine")
						err := c.dataHandler.HandleDataChunk(msgData)
						if err != nil {
						c.logger.LogDebug(fmt.Sprintf("Error handling data chunk: %v", err))
						} else {
							c.logger.LogDebug("HandleDataChunk completed successfully")
						}
					} else {
						c.logger.LogDebug("ERROR: dataHandler is nil, cannot handle data chunk")
					}
			}()
			}()
		})
		
		// Add buffer threshold callback
		c.connection.GetDataChannel().SetBufferedAmountLowThreshold(262144) // 256KB
		c.connection.GetDataChannel().OnBufferedAmountLow(func() {
			c.logger.LogDebug("Data channel buffer amount low event triggered")
		})
	} else {
		c.logger.LogDebug("Data channel is nil, cannot set up handler")
	}
	
	c.logger.LogDebug("Channel handlers setup complete")
}

// SendChatMessage sends a chat message on the control channel
func (c *Channels) SendChatMessage(text string) error {
	c.logger.LogDebug("SendChatMessage called with text: " + text)
	
	if c.connection.GetControlChannel() == nil {
		c.logger.LogDebug("ERROR: Control channel is nil")
		return fmt.Errorf("control channel not initialized")
	}

	// Log the control channel state
	c.logger.LogDebug(fmt.Sprintf("Control channel state: %s", c.connection.GetControlChannel().ReadyState().String()))
	c.logger.LogDebug(fmt.Sprintf("Control channel buffered amount: %d", c.connection.GetControlChannel().BufferedAmount()))

	// Create the message
	message := struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}{
		Type:    "message",
		Content: text,
	}

	// Marshal the message
	messageJSON, err := json.Marshal(message)
	if err != nil {
		c.logger.LogDebug(fmt.Sprintf("ERROR: Failed to marshal message: %v", err))
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	// Log the message being sent
	c.logger.LogDebug(fmt.Sprintf("WEBRTC CHANNEL SENDING MESSAGE: %s", string(messageJSON)))

	// Send the message
	err = c.connection.GetControlChannel().SendText(string(messageJSON))
	if err != nil {
		c.logger.LogDebug(fmt.Sprintf("ERROR: Failed to send message: %v", err))
		return fmt.Errorf("failed to send message: %v", err)
	}

	c.logger.LogDebug("Chat message sent successfully via WebRTC data channel")
	return nil
}

// SendCapabilities sends capabilities information
func (c *Channels) SendCapabilities(maxChunkSize int) error {
	if c.connection.GetControlChannel() == nil {
		return fmt.Errorf("control channel not initialized")
	}

	// Create the message
	message := struct {
		Type         string `json:"type"`
		MaxChunkSize int    `json:"maxChunkSize"`
	}{
		Type:         "capabilities",
		MaxChunkSize: maxChunkSize,
	}

	// Marshal the message
	messageJSON, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %v", err)
	}

	// Send the message
	err = c.connection.GetControlChannel().SendText(string(messageJSON))
	if err != nil {
		return fmt.Errorf("failed to send capabilities: %v", err)
	}

	return nil
}
