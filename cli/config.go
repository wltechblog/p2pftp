package main

// Configuration constants for the application
const (
	// WebRTC configuration
	maxWebRTCMessageSize   = 262144 // 256KB - Maximum size for WebRTC messages
	maxSupportedChunkSize  = 262144 // 256KB - Maximum supported chunk size
	fixedChunkSize         = 262144 // 256KB - Fixed chunk size for consistency
	
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