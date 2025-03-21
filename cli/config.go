package main

// Configuration constants for the application
const (
// WebRTC configuration
maxWebRTCMessageSize   = 262144 // 256KB - Maximum size for WebRTC messages
maxSupportedChunkSize  = 262136 // 256KB - 8 bytes - Maximum supported chunk size (accounting for frame header)
fixedChunkSize         = 262136 // 256KB - 8 bytes - Fixed chunk size (accounting for frame header)
	
	// UI configuration
	maxHistoryEntries      = 5      // Maximum number of history entries to display
	
	// Network configuration
	defaultServerAddress   = "localhost:8089" // Default server address
	
	// File transfer configuration
	defaultWindowSize      = 64     // Default sliding window size
	minCongestionWindow    = 8      // Minimum congestion window size
	maxRetransmitAttempts  = 5      // Maximum number of retransmission attempts
	
	// Timing configuration
	retransmitInterval     = 3      // Seconds to wait before retransmitting
	progressUpdateInterval = 100    // Milliseconds between progress updates
	channelCloseWaitTime   = 100    // Milliseconds to wait after sending before closing
)
