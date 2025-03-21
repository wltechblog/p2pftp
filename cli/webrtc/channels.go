package webrtc

import (
	"encoding/json"
	"fmt"

	pionwebrtc "github.com/pion/webrtc/v3"
)

// Channels handles WebRTC data channels
type Channels struct {
	connection *Connection
	logger     Logger
	msgHandler MessageHandler
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
			c.logger.LogDebug(fmt.Sprintf("Received control message: %s", string(msg.Data)))
			
			// Try to parse the message to see if it's a chat message
			var message map[string]interface{}
			err := json.Unmarshal(msg.Data, &message)
			if err == nil {
				msgType, ok := message["type"].(string)
				if ok && msgType == "message" {
					content, ok := message["content"].(string)
					if ok {
						c.logger.LogDebug(fmt.Sprintf("Received chat message with content: %s", content))
					}
				}
			}
			
			if c.msgHandler != nil {
				c.logger.LogDebug("Calling msgHandler.HandleControlMessage")
				err := c.msgHandler.HandleControlMessage(msg.Data)
				if err != nil {
					c.logger.LogDebug(fmt.Sprintf("Error handling control message: %v", err))
				} else {
					c.logger.LogDebug("Message handled successfully")
				}
			} else {
				c.logger.LogDebug("msgHandler is nil, cannot handle message")
			}
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
			c.logger.LogDebug(fmt.Sprintf("Received data chunk: %d bytes", len(msg.Data)))
			
			if c.dataHandler != nil {
				err := c.dataHandler.HandleDataChunk(msg.Data)
				if err != nil {
					c.logger.LogDebug(fmt.Sprintf("Error handling data chunk: %v", err))
				}
			}
		})
	} else {
		c.logger.LogDebug("Data channel is nil, cannot set up handler")
	}
	
	c.logger.LogDebug("Channel handlers setup complete")
}

// SendChatMessage sends a chat message on the control channel
func (c *Channels) SendChatMessage(text string) error {
	if c.connection.GetControlChannel() == nil {
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
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	// Log the message being sent
	c.logger.LogDebug(fmt.Sprintf("Sending chat message: %s", string(messageJSON)))

	// Send the message
	err = c.connection.GetControlChannel().SendText(string(messageJSON))
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	c.logger.LogDebug("Chat message sent successfully")
	return nil
}

// SendCapabilities sends capabilities information
func (c *Channels) SendCapabilities(maxChunkSize int) error {
	if c.connection.GetControlChannel() == nil {
		return fmt.Errorf("control channel not initialized")
	}

	// Create the capabilities message
	capabilities := struct {
		Type         string `json:"type"`
		MaxChunkSize int    `json:"maxChunkSize"`
	}{
		Type:         "capabilities",
		MaxChunkSize: maxChunkSize,
	}

	// Marshal the capabilities
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return fmt.Errorf("failed to marshal capabilities: %v", err)
	}

	// Send the capabilities
	err = c.connection.GetControlChannel().SendText(string(capabilitiesJSON))
	if err != nil {
		return fmt.Errorf("failed to send capabilities: %v", err)
	}

	return nil
}