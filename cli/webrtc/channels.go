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

// handleControlMessage processes a control channel message
func (c *Channels) handleControlMessage(msgData []byte) {
    c.logger.LogDebug("====== WEBRTC MESSAGE RECEIVED START ======")
    c.logger.LogDebug(fmt.Sprintf("Raw message data: %s", string(msgData)))
    
    // Try to parse the message to see if it's valid JSON
    var parsed map[string]interface{}
    if err := json.Unmarshal(msgData, &parsed); err != nil {
        c.logger.LogDebug(fmt.Sprintf("ERROR: Failed to parse JSON: %v", err))
        c.logger.LogDebug(fmt.Sprintf("Raw message that failed to parse: %s", string(msgData)))
        return
    }
    
    c.logger.LogDebug(fmt.Sprintf("Successfully parsed JSON message: %+v", parsed))
    
    // Handle the message type
    msgType, ok := parsed["type"].(string)
    if ok {
        c.logger.LogDebug(fmt.Sprintf("Message type: %s", msgType))
        if msgType == "message" {
            if content, ok := parsed["content"].(string); ok {
                c.logger.AppendChat(content)
            }
        }
    }
    
    // Forward to message handler
    if c.msgHandler != nil {
        c.logger.LogDebug("Calling HandleControlMessage")
        if err := c.msgHandler.HandleControlMessage(msgData); err != nil {
            c.logger.LogDebug(fmt.Sprintf("Error handling control message: %v", err))
        } else {
            c.logger.LogDebug("HandleControlMessage completed successfully")
        }
    } else {
        c.logger.LogDebug("ERROR: msgHandler is nil, cannot handle control message")
    }
}

// handleDataChunk processes a data channel chunk
func (c *Channels) handleDataChunk(msgData []byte) {
    c.logger.LogDebug(fmt.Sprintf("Processing data chunk: %d bytes", len(msgData)))
    
    if c.dataHandler != nil {
        c.logger.LogDebug("Calling HandleDataChunk")
        if err := c.dataHandler.HandleDataChunk(msgData); err != nil {
            c.logger.LogDebug(fmt.Sprintf("Error handling data chunk: %v", err))
        } else {
            c.logger.LogDebug("HandleDataChunk completed successfully")
        }
    } else {
        c.logger.LogDebug("ERROR: dataHandler is nil, cannot handle data chunk")
    }
}

// SetupChannelHandlers sets up handlers for the data channels
func (c *Channels) SetupChannelHandlers() {
    c.logger.LogDebug("Setting up channel handlers")

    // Set up control channel handler
    if c.connection.GetControlChannel() != nil {
        c.logger.LogDebug(fmt.Sprintf("Control channel state: %s", c.connection.GetControlChannel().ReadyState().String()))

        c.connection.GetControlChannel().OnMessage(func(msg pionwebrtc.DataChannelMessage) {
            // Process the message in a single goroutine to maintain order
            go c.handleControlMessage(msg.Data)
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
            // Process the chunk in a single goroutine to maintain order
            go c.handleDataChunk(msg.Data)
        })

        // Add buffer threshold callback
        c.connection.GetDataChannel().SetBufferedAmountLowThreshold(262136) // 256KB - 8 bytes for header
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
        MaxChunkSize: maxChunkSize - 8, // Account for 8-byte header
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
