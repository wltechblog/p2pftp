# P2PFTP Protocol Documentation

This document describes the communication and file transfer protocol used by P2PFTP, a peer-to-peer file transfer application. This protocol documentation can be used to test compatibility with other implementations.

## Overview

P2PFTP uses a combination of WebSocket signaling and WebRTC data channels to establish secure peer-to-peer connections for file transfers and messaging. The protocol consists of three main components:

1. **Signaling Protocol**: Used to establish the WebRTC connection
2. **Control Channel Protocol**: Used for metadata exchange and transfer control
3. **Data Channel Protocol**: Used for binary data transfer

## 1. Signaling Protocol

The signaling protocol uses WebSockets to exchange connection information between peers.

### Connection Establishment

1. **Token Assignment**:
   - Client connects to server via WebSocket
   - Server assigns a unique token to the client
   - Message format: `{"type": "token", "token": "<unique-token>"}`

2. **Connection Request**:
   - Client A sends connection request with Client B's token
   - Message format: `{"type": "connect", "peerToken": "<peer-token>"}`
   - Server forwards request to Client B: `{"type": "request", "token": "<client-a-token>"}`

3. **Connection Response**:
   - Client B accepts: `{"type": "accept", "peerToken": "<client-a-token>"}`
   - Client B rejects: `{"type": "reject", "peerToken": "<client-a-token>"}`
   - Server notifies Client A: `{"type": "accepted", "token": "<client-b-token>"}` or `{"type": "rejected", "token": "<client-b-token>"}`

### WebRTC Signaling

1. **SDP Offer Exchange**:
   - Initiator creates and sends offer: `{"type": "offer", "peerToken": "<peer-token>", "sdp": "<sdp-offer-json>"}`
   - Server forwards to peer: `{"type": "offer", "token": "<initiator-token>", "sdp": "<sdp-offer-json>"}`
   - Receiver creates and sends answer: `{"type": "answer", "peerToken": "<initiator-token>", "sdp": "<sdp-answer-json>"}`
   - Server forwards to initiator: `{"type": "answer", "token": "<receiver-token>", "sdp": "<sdp-answer-json>"}`

2. **ICE Candidate Exchange**:
   - Each peer sends ICE candidates: `{"type": "ice", "peerToken": "<peer-token>", "ice": "<ice-candidate-json>"}`
   - Server forwards to other peer: `{"type": "ice", "token": "<sender-token>", "ice": "<ice-candidate-json>"}`

## 2. WebRTC Data Channels

P2PFTP uses two WebRTC data channels:

1. **Control Channel** (ID: 1, Label: "p2pftp-control"):
   - Ordered delivery
   - Used for JSON messages (metadata, control commands)

2. **Data Channel** (ID: 2, Label: "p2pftp-data"):
   - Ordered delivery
   - Binary mode (arraybuffer)
   - Used for file chunk transfer

## 3. Control Channel Protocol

The control channel exchanges JSON messages for file transfer coordination.

### Message Types

1. **Capabilities Exchange**:
   - Sent after connection establishment
   - Format: `{"type": "capabilities", "maxChunkSize": <max-chunk-size>}`
   - Response: `{"type": "capabilities-ack", "negotiatedChunkSize": <negotiated-size>}`

2. **Chat Message**:
   - Format: `{"type": "message", "content": "<message-text>"}`

3. **File Transfer Initiation**:
   - Format:
     ```json
     {
       "type": "file-info",
       "info": {
         "name": "<filename>",
         "size": <file-size-in-bytes>,
         "md5": "<md5-hash>"
       }
     }
     ```

4. **Chunk Information**:
   - Sent before each chunk on the data channel
   - Format:
     ```json
     {
       "type": "chunk-info",
       "sequence": <chunk-sequence-number>,
       "totalChunks": <total-chunks>,
       "size": <chunk-size-in-bytes>
     }
     ```

5. **Chunk Acknowledgment**:
   - Sent by receiver to confirm chunk receipt
   - Format: `{"type": "chunk-confirm", "sequence": <chunk-sequence-number>}`

6. **Missing Chunks Request**:
   - Sent by receiver to request retransmission of specific chunks
   - Used for explicit recovery of lost or corrupted chunks
   - Format: `{"type": "request-chunks", "sequences": [<sequence1>, <sequence2>, ...]}`
   - The sender should prioritize these chunks for immediate retransmission

7. **Transfer Completion**:
   - Format: `{"type": "file-complete"}`

## 4. Data Channel Protocol

The data channel transfers binary data using a framed format.

### Chunk Format

Each chunk is framed with metadata:
```
[4 bytes sequence number][4 bytes length][data bytes]
```

- **Sequence Number**: 4-byte big-endian integer (0-based index)
- **Length**: 4-byte big-endian integer (size of data in bytes)
- **Data**: Raw binary data of the specified length

## 5. Transfer Protocol

P2PFTP uses a sliding window protocol with selective acknowledgment for reliable file transfer.

### Sender Process

1. Calculate total chunks based on file size and negotiated chunk size
2. Send file metadata via control channel
3. For each chunk in the sliding window:
   - Send chunk info via control channel
   - Send framed chunk data via data channel
   - Track unacknowledged chunks with timestamps
4. Process acknowledgments and adjust window:
   - Update the last acknowledged sequence
   - Remove chunks from the unacknowledged list
   - Advance the sliding window
5. Handle retransmission requests:
   - Prioritize explicitly requested chunks
   - Add requested chunks to the retransmission queue
   - Send requested chunks immediately when possible
6. Periodically check for timed-out chunks:
   - Identify chunks that haven't been acknowledged within timeout period
   - Add timed-out chunks to the retransmission queue
7. Send completion message when all chunks are acknowledged

### Receiver Process

1. Receive file metadata and prepare buffer or file handle
2. For each received chunk:
   - Validate sequence number and size
   - Store in buffer or write to file at the correct offset
   - Send acknowledgment via the control channel
   - Process out-of-order chunks correctly
3. Actively track missing chunks by:
   - Maintaining a map of missing sequence numbers
   - Periodically checking for gaps in the received sequence
   - Explicitly requesting missing chunks via the "request-chunks" message
4. Handle unexpected or out-of-sequence chunks:
   - Accept and process valid chunks even if they arrive out of order
   - Track the highest sequence number received to detect gaps
5. Verify file integrity using MD5 hash when all chunks are received
6. Complete transfer when all chunks are received

## 6. Flow Control and Congestion Control

P2PFTP implements TCP-like flow control and congestion control:

1. **Sliding Window**: Default window size of 64 chunks
2. **Slow Start**: Exponential window growth up to 32 chunks
3. **Congestion Avoidance**: Additive increase after slow start
4. **Timeout Detection**: Chunks not acknowledged within 3 seconds are considered lost
5. **Multiplicative Decrease**: Window size reduced by 30% after consecutive timeouts

## 7. Error Handling

1. **Connection Errors**:
   - ICE connection failures trigger reconnection attempts
   - Persistent failures result in disconnection
   - Channel state is monitored continuously during transfers

2. **Transfer Errors**:
   - Missing chunks are explicitly requested for retransmission using the "request-chunks" message
   - Corrupted chunks are detected by size mismatch and added to the missing chunks list
   - Out-of-order delivery is handled gracefully by writing chunks at the correct file offset
   - Unexpected chunks are processed if valid, improving resilience to network issues
   - Periodic checks identify gaps in the received sequence
   - File integrity is verified with MD5 hash after all chunks are received

## 8. Security Considerations

1. **End-to-End Encryption**: All data is encrypted using WebRTC's built-in DTLS
2. **Token-Based Authentication**: Unique tokens prevent unauthorized connections
3. **No Server Storage**: Files are transferred directly between peers, with no server storage

## 9. Implementation Requirements

To implement a compatible client:

1. Support WebSocket signaling protocol
2. Implement WebRTC with data channel support
3. Support the control channel message formats:
   - Implement all message types including "request-chunks" for explicit retransmission
   - Handle out-of-order message processing
4. Implement the data channel framing format:
   - 4-byte sequence number + 4-byte length + data
   - Support writing chunks at correct file offsets regardless of arrival order
5. Support sliding window transfer with selective acknowledgment:
   - Track unacknowledged chunks with timestamps
   - Maintain a retransmission queue
   - Process explicit retransmission requests
6. Implement robust error handling:
   - Detect and request missing chunks
   - Process unexpected but valid chunks
   - Handle corrupted chunks by requesting retransmission
   - Monitor channel state during transfers

## 10. Testing Compatibility

To test compatibility with P2PFTP:

1. Connect to a P2PFTP signaling server
2. Complete the signaling process
3. Establish WebRTC data channels
4. Exchange capabilities
5. Transfer a test file in both directions
6. Verify file integrity using MD5 hash