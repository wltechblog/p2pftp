// Package transfer handles file transfers and associated operations
package transfer

// Logger provides logging functionality for all transfer operations
type Logger interface {
    // LogDebug logs a debug message
    LogDebug(msg string)
    // ShowError displays an error message
    ShowError(msg string)
    // ShowChat displays a chat message with sender information
    ShowChat(from string, msg string)
}

// ProgressCallback is called to update transfer progress
type ProgressCallback func(status string, direction string)

// MessageHandler defines the interface for handling WebRTC control messages
type MessageHandler interface {
    HandleControlMessage(msg []byte) error
}

// DataHandler defines the interface for handling WebRTC data chunks
type DataHandler interface {
    HandleDataChunk(data []byte) error
}
