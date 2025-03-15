package webrtc

import (
	"encoding/json"
	"fmt"

	"github.com/pion/webrtc/v3"
)

// MessageHandler handles messages received on the control channel
type MessageHandler interface {
	HandleFileInfo(data map[string]interface{}) error
	HandleFileInfoUpdate(data map[string]interface{}) error
	HandleFileComplete() error
	HandleChunkConfirm(sequence int) error
}

// ChunkHandler handles binary chunks received on the data channel
type ChunkHandler interface {
	HandleBinaryChunkData(sequence int, total int, size int, data []byte) error
}

// Channels manages WebRTC data channels
type Channels struct {
	connection    *Connection
	logger        Logger
	messageHandler MessageHandler
	chunkHandler   ChunkHandler
}

// NewChannels creates a new channels manager
func NewChannels(
	connection *Connection,
	logger Logger,
	messageHandler MessageHandler,
	chunkHandler ChunkHandler,
) *Channels {
	return &Channels{
		connection:    connection,
		logger:        logger,
		messageHandler: messageHandler,
		chunkHandler:   chunkHandler,
	}
}

// SetupChannelHandlers sets up handlers for data channels
func (c *Channels) SetupChannelHandlers() {
	// Set up control channel message handler
	c.connection.state.ControlChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !msg.IsString {
			c.logger.LogDebug("Received binary data on control channel, ignoring")
			return
		}

		// Parse the message
		var data map[string]interface{}
		err := json.Unmarshal(msg.Data, &data)
		if err != nil {
			c.logger.LogDebug(fmt.Sprintf("Failed to parse control message: %v", err))
			return
		}

		// Get the message type
		msgType, ok := data["type"].(string)
		if !ok {
			c.logger.LogDebug("Control message missing type field")
			return
		}

		// Handle the message based on its type
		switch msgType {
		case "file-info":
			if c.messageHandler != nil {
				err := c.messageHandler.HandleFileInfo(data)
				if err != nil {
					c.logger.LogDebug(fmt.Sprintf("Error handling file info: %v", err))
				}
			}
		case "file-info-update":
			if c.messageHandler != nil {
				err := c.messageHandler.HandleFileInfoUpdate(data)
				if err != nil {
					c.logger.LogDebug(fmt.Sprintf("Error handling file info update: %v", err))
				}
			}
		case "file-complete":
			if c.messageHandler != nil {
				err := c.messageHandler.HandleFileComplete()
				if err != nil {
					c.logger.LogDebug(fmt.Sprintf("Error handling file complete: %v", err))
				}
			}
		case "chunk-confirm":
			if c.messageHandler != nil {
				sequence, ok := data["sequence"].(float64)
				if !ok {
					c.logger.LogDebug("Chunk confirm missing sequence field")
					return
				}
				err := c.messageHandler.HandleChunkConfirm(int(sequence))
				if err != nil {
					c.logger.LogDebug(fmt.Sprintf("Error handling chunk confirm: %v", err))
				}
			}
		case "chunk-info":
			// This is handled by the data channel
			sequence, _ := data["sequence"].(float64)
			totalChunks, _ := data["totalChunks"].(float64)
			size, _ := data["size"].(float64)
			c.logger.LogDebug(fmt.Sprintf("Received chunk info: sequence=%d, totalChunks=%d, size=%d",
				int(sequence), int(totalChunks), int(size)))
		case "chunk-request":
			// Handle chunk request
			sequence, ok := data["sequence"].(float64)
			if !ok {
				c.logger.LogDebug("Chunk request missing sequence field")
				return
			}
			c.logger.LogDebug(fmt.Sprintf("Received chunk request: sequence=%d", int(sequence)))
			// TODO: Implement chunk request handling
		case "capabilities":
			// Handle capabilities message
			maxChunkSize, ok := data["maxChunkSize"].(float64)
			if !ok {
				c.logger.LogDebug("Capabilities missing maxChunkSize field")
				return
			}
			c.logger.LogDebug(fmt.Sprintf("Received capabilities: maxChunkSize=%d", int(maxChunkSize)))
			// TODO: Implement capabilities handling
		case "capabilities-ack":
			// Handle capabilities acknowledgment
			negotiatedSize, ok := data["negotiatedChunkSize"].(float64)
			if !ok {
				c.logger.LogDebug("Capabilities ack missing negotiatedChunkSize field")
				return
			}
			c.logger.LogDebug(fmt.Sprintf("Received capabilities ack: negotiatedChunkSize=%d", int(negotiatedSize)))
			// TODO: Implement capabilities ack handling
		case "message":
			// Handle chat message
			content, ok := data["content"].(string)
			if !ok {
				c.logger.LogDebug("Message missing content field")
				return
			}
			c.logger.LogDebug(fmt.Sprintf("Received chat message: %s", content))
			// TODO: Implement chat message handling
		default:
			c.logger.LogDebug(fmt.Sprintf("Unknown control message type: %s", msgType))
		}
	})

	// Set up data channel message handler
	c.connection.state.DataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		// We expect binary data on this channel
		if msg.IsString {
			c.logger.LogDebug("Received string data on binary channel, ignoring")
			return
		}

		// Check if we have enough data for the header (8 bytes)
		if len(msg.Data) < 8 {
			c.logger.LogDebug(fmt.Sprintf("Received binary data too small for header: %d bytes", len(msg.Data)))
			return
		}

		// Parse the header
		sequence := int(msg.Data[0])<<24 | int(msg.Data[1])<<16 | int(msg.Data[2])<<8 | int(msg.Data[3])
		dataLength := int(msg.Data[4])<<24 | int(msg.Data[5])<<16 | int(msg.Data[6])<<8 | int(msg.Data[7])

		// Check if we have enough data for the payload
		if len(msg.Data) < 8+dataLength {
			c.logger.LogDebug(fmt.Sprintf("Received binary data too small for payload: %d bytes, expected %d bytes",
				len(msg.Data), 8+dataLength))
			return
		}

		// Extract the payload
		data := msg.Data[8 : 8+dataLength]

		// Log the received chunk
		c.logger.LogDebug(fmt.Sprintf("Received binary chunk: sequence=%d, size=%d", sequence, dataLength))

		// Process the binary chunk
		if c.chunkHandler != nil {
			err := c.chunkHandler.HandleBinaryChunkData(sequence, 0, dataLength, data)
			if err != nil {
				c.logger.LogDebug(fmt.Sprintf("Error handling binary chunk: %v", err))
			}
		}
	})
}

// SendChatMessage sends a chat message
func (c *Channels) SendChatMessage(text string) error {
	// Check if data channel is initialized and open
	if c.connection.state.DataChannel == nil {
		return fmt.Errorf("data channel not initialized, connection may not be fully established")
	}
	
	// Check data channel state
	if c.connection.state.DataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel is not in open state (current state: %s)",
			c.connection.state.DataChannel.ReadyState().String())
	}

	// Create the message
	msg := struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}{
		Type:    "message",
		Content: text,
	}

	// Marshal the message
	msgJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	// Send the message
	err = c.connection.state.DataChannel.SendText(string(msgJSON))
	if err != nil {
		c.logger.LogDebug(fmt.Sprintf("Error sending chat message: %v", err))
		return fmt.Errorf("failed to send message: %v", err)
	}

	return nil
}