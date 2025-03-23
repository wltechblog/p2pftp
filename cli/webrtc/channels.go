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

    // Set up control channel handler (for metadata, capabilities, etc.)
    if c.connection.GetControlChannel() != nil {
        ch := c.connection.GetControlChannel()
        c.logger.LogDebug(fmt.Sprintf("[Control] Channel state: %s", ch.ReadyState().String()))

        ch.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
            if !msg.IsString {
                c.logger.LogDebug("[Control] WARNING: Received unexpected binary data")
                return
            }
            if c.msgHandler != nil {
                if err := c.msgHandler.HandleControlMessage(msg.Data); err != nil {
                    c.logger.LogDebug(fmt.Sprintf("[Control] Error handling message: %v", err))
                }
            }
        })

        ch.SetBufferedAmountLowThreshold(controlBufferSize)
        ch.OnBufferedAmountLow(func() {
            c.logger.LogDebug(fmt.Sprintf("[Control] Buffer below threshold (%d bytes)", ch.BufferedAmount()))
        })
    } else {
        c.logger.LogDebug("[Control] Channel is nil, cannot set up handler")
    }

    // Set up data channel handler (for file chunks and chat messages)
    if c.connection.GetDataChannel() != nil {
        ch := c.connection.GetDataChannel()
        c.logger.LogDebug(fmt.Sprintf("[Data] Channel state: %s", ch.ReadyState().String()))

        // Use default chunk size for initial buffering
        ch.SetBufferedAmountLowThreshold(defaultChunkSize)
        
        ch.OnMessage(func(msg pionwebrtc.DataChannelMessage) {
            msgSize := len(msg.Data)
            if msgSize > maxMessageSize {
                c.logger.LogDebug(fmt.Sprintf("[Data] ERROR: Message too large: %d bytes", msgSize))
                return
            }

            if msg.IsString {
                // Parse and handle string messages (like chat) via control message handler
                if c.msgHandler != nil {
                    // Log message content for debugging
                    c.logger.LogDebug(fmt.Sprintf("[Data] Received string message: %s", string(msg.Data)))
                    if err := c.msgHandler.HandleControlMessage(msg.Data); err != nil {
                        c.logger.LogDebug(fmt.Sprintf("[Data] Error handling string message: %v", err))
                    }
                }
            } else {
                // Handle binary data (file chunks) via data handler
                if c.dataHandler != nil && msgSize >= 8 {
                    if err := c.dataHandler.HandleDataChunk(msg.Data); err != nil {
                        c.logger.LogDebug(fmt.Sprintf("[Data] Error handling binary chunk: %v", err))
                    }
                } else {
                    c.logger.LogDebug(fmt.Sprintf("[Data] Invalid binary message size: %d bytes", msgSize))
                }
            }
        })

        ch.OnBufferedAmountLow(func() {
            c.logger.LogDebug(fmt.Sprintf("[Data] Buffer below threshold (%d bytes)", ch.BufferedAmount()))
        })
    } else {
        c.logger.LogDebug("[Data] Channel is nil, cannot set up handler")
    }

    c.logger.LogDebug("Channel handlers setup complete")
}

// SendChatMessage sends a chat message on the data channel
func (c *Channels) SendChatMessage(text string) error {
    c.logger.LogDebug("[Chat] Sending message: " + text)

    ch := c.connection.GetDataChannel()
    if ch == nil {
        c.logger.LogDebug("[Chat] ERROR: Data channel is nil")
        return fmt.Errorf("[Chat] Data channel not initialized")
    }

    // Check channel state
    if ch.ReadyState() != pionwebrtc.DataChannelStateOpen {
        c.logger.LogDebug("[Chat] ERROR: Data channel not open for sending")
        return fmt.Errorf("[Chat] Data channel not in open state")
    }

    c.logger.LogDebug(fmt.Sprintf("[Chat] Data channel state: %s", ch.ReadyState().String()))

    // Create message
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
        c.logger.LogDebug(fmt.Sprintf("[Chat] ERROR: Failed to marshal message: %v", err))
        return fmt.Errorf("[Chat] Failed to marshal message: %v", err)
    }

    // Check message size
    if len(messageJSON) > maxMessageSize {
        c.logger.LogDebug(fmt.Sprintf("[Chat] ERROR: Message too large: %d bytes", len(messageJSON)))
        return fmt.Errorf("[Chat] Message exceeds maximum size")
    }

    // Log the message being sent
    c.logger.LogDebug(fmt.Sprintf("[Chat] Sending: %s", string(messageJSON)))

    // Send the message
    err = ch.SendText(string(messageJSON))
    if err != nil {
        c.logger.LogDebug(fmt.Sprintf("[Chat] ERROR: Failed to send message: %v", err))
        return fmt.Errorf("[Chat] Failed to send message: %v", err)
    }

    c.logger.LogDebug("[Chat] Message sent successfully")
    return nil
}

// SendCapabilities sends capabilities information
func (c *Channels) SendCapabilities(maxChunkSize int) error {
    if c.connection.GetControlChannel() == nil {
        return fmt.Errorf("control channel not initialized")
    }

    // Ensure the control channel is ready
    if c.connection.GetControlChannel().ReadyState() != pionwebrtc.DataChannelStateOpen {
        c.logger.LogDebug("[Control] Channel not open for sending capabilities")
        return fmt.Errorf("[Control] Channel not in open state")
    }

    // Ensure max chunk size is within limits
    if maxChunkSize > maxMessageSize {
        maxChunkSize = maxMessageSize - 8 // Account for header
    }

    message := struct {
        Type         string `json:"type"`
        MaxChunkSize int    `json:"maxChunkSize"`
    }{
        Type:         "capabilities",
        MaxChunkSize: maxChunkSize,
    }

    messageJSON, err := json.Marshal(message)
    if err != nil {
        c.logger.LogDebug(fmt.Sprintf("[Control] ERROR: Failed to marshal capabilities: %v", err))
        return fmt.Errorf("[Control] Failed to marshal capabilities: %v", err)
    }

    c.logger.LogDebug(fmt.Sprintf("[Control] Sending capabilities: %s", string(messageJSON)))
    if err := c.connection.GetControlChannel().SendText(string(messageJSON)); err != nil {
        c.logger.LogDebug(fmt.Sprintf("[Control] ERROR: Failed to send capabilities: %v", err))
        return fmt.Errorf("[Control] Failed to send capabilities: %v", err)
    }

    c.logger.LogDebug("[Control] Capabilities sent successfully")
    return nil
}
