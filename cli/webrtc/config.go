package webrtc

// Constants for data channel configuration
const (
    defaultChunkSize    = 16384 // 16KB default chunk size from web client
    maxMessageSize      = 65536 // 64KB maximum WebRTC message size
    controlBufferSize   = 4096  // 4KB for control messages
)

// min returns the minimum of two integers
func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
