# Active Context

## Current Focus
We are revisiting the P2PFTP implementation with a focus on simplifying the protocol by leveraging WebRTC's built-in reliability guarantees and ensuring consistent implementation across clients. We have removed the web interface to focus on the CLI implementation first.

## Identified Issues

### 1. Client Implementation Issues
- The command-line client using TUI (Text User Interface) has been experiencing deadlocks and race conditions
- These issues cause the client to crash or hang during operation
- The current UI implementation is overly complex for our needs

### 2. Protocol Implementation Differences
- Inconsistencies between the CLI and web interface implementations
- These differences lead to compatibility issues and failed transfers
- Need for a more rigid and clear specification that both implementations follow exactly

### 3. Data Transfer Issues
- Data channel configuration mismatch between Go and JavaScript implementations
- The Go implementation is not setting the `negotiated` flag and pre-negotiated IDs as specified in the protocol
- Capabilities exchange implementation is incomplete in the Go client
- Chunk size mismatch between implementations (Go: 8184 bytes, JavaScript: 65528 bytes)

## Recent Protocol Decisions

### Revised Chunk Size Negotiation
- Capabilities exchange is MANDATORY before any file transfer begins
- Both peers must send their maximum supported chunk size in the capabilities message
- The negotiated chunk size is the minimum of the two values
- If a peer supports larger chunks than our maximum, log an INFO message to indicate potential optimization opportunity
- Both peers must acknowledge the negotiated size before transfer begins
- If no capabilities exchange occurs within 5 seconds of connection establishment, log an error and abort the transfer

### Simplified Protocol Leveraging WebRTC Reliability
Since we're using reliable WebRTC channels (ordered: true, maxRetransmits: null), we've decided to simplify our protocol:

1. **Simplified Chunk Handling**:
   - Minimal chunk header (sequence number and size)
   - No need to store chunks for retransmission (WebRTC handles this)
   - No need for explicit chunk-by-chunk acknowledgment

2. **Simplified Flow Control**:
   - Basic window control with periodic progress updates instead of per-chunk acknowledgments
   - Rely primarily on WebRTC's built-in congestion control
   - Monitor data channel bufferedAmount to avoid overwhelming the channel

3. **Simplified Error Handling**:
   - Monitor WebRTC connection state
   - Pause transfer if connection is lost
   - Resume from last progress point if reconnection succeeds

4. **End-to-End Verification**:
   - Still perform MD5 verification of the complete file
   - No need for per-chunk checksums

## Implementation Strategy
We've decided to:
1. ✅ Remove the web implementation to focus on the CLI client
2. Fix the data channel configuration in the Go implementation to match the protocol specification
3. Implement proper capabilities exchange in the Go client
4. Standardize chunk size limits between implementations
5. Test the CLI implementation thoroughly before rebuilding the web interface

## Next Steps
1. ✅ Update the PROTOCOL.md document to reflect our simplified approach
2. ✅ Validate the signaling server implementation against the updated specification
3. ✅ Remove the web implementation to focus on the CLI client
4. 🔄 Fix the data channel configuration in the Go implementation:
   - Set the `negotiated` flag and pre-negotiated IDs as specified in the protocol
   - Implement proper capabilities exchange with acknowledgment
   - Standardize chunk size limits to match the protocol specification
5. Test the CLI implementation thoroughly
6. Once the CLI implementation is stable, rebuild the web interface following the same protocol