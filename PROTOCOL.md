# P2PFTP Protocol Documentation (v2)

This document describes the communication and file transfer protocol used by P2PFTP, a peer-to-peer file transfer application. This protocol documentation can be used to test compatibility with other implementations.

## Overview

P2PFTP uses a combination of WebSocket signaling and WebRTC data channels to establish secure peer-to-peer connections for file transfers and messaging. The protocol consists of three main components:

1. **Signaling Protocol**: Used to establish the WebRTC connection
2. **Control Channel Protocol**: Used for metadata exchange and transfer control
3. **Data Channel Protocol**: Used for binary data transfer

## 1. Signaling Protocol

The signaling protocol uses WebSockets to exchange connection information between peers. The signaling server is always behind a HTTPS proxy, so we always use wss and never ws.

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

P2PFTP uses two WebRTC data channels with specific configurations:

1. **Control Channel** (ID: 1, Label: "p2pftp-control"):
   - Negotiated: true (pre-negotiated ID)
   - Ordered: true (guaranteed order delivery)
   - MaxPacketLifeTime: null (no time limit)
   - MaxRetransmits: null (unlimited retransmissions)
   - Protocol: "" (no subprotocol)
   - Priority: "high"
   - BufferSize: 262144 bytes (256KB)

2. **Data Channel** (ID: 2, Label: "p2pftp-data"):
   - Negotiated: true (pre-negotiated ID)
   - Ordered: true (guaranteed order delivery)
   - MaxPacketLifeTime: null (no time limit)
   - MaxRetransmits: null (unlimited retransmissions)
   - Protocol: "" (no subprotocol)
   - Priority: "medium"
   - BufferSize: 1048576 bytes (1MB)
   - BinaryType: "arraybuffer"

## 3. Control Channel Protocol

The control channel exchanges JSON messages for file transfer coordination.

### Message Types

1. **Capabilities Exchange** (MANDATORY):
   - Must be sent immediately after connection establishment
   - Must be completed before any file transfer begins
   - Format: `{"type": "capabilities", "maxChunkSize": <max-chunk-size>}`
   - Response: `{"type": "capabilities-ack", "negotiatedChunkSize": <negotiated-size>}`
   - If no capabilities exchange occurs within 5 seconds, the connection should be considered invalid
   - Chunk Size Parameters:
     - Default Chunk Size: 16384 bytes (16KB)
     - Minimum Chunk Size: 4096 bytes (4KB)
     - Maximum Chunk Size: 262144 bytes (256KB)
   - Negotiation Algorithm:
     - The negotiated chunk size is the minimum of the two peers' maximum supported sizes
     - If a peer supports larger chunks than our maximum, log an INFO message
     - Both peers must acknowledge the negotiated size before transfer begins

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

4. **Progress Update**:
   - Sent periodically (every 1 second or every 32 chunks) by the receiver
   - Format:
     ```json
     {
       "type": "progress-update",
       "bytesReceived": <bytes-received>,
       "highestSequence": <highest-sequence-received>
     }
     ```

5. **Transfer Completion**:
   - Sent by sender when all chunks have been sent
   - Format: `{"type": "file-complete"}`

6. **File Verification**:
   - Sent by receiver after MD5 verification
   - Format: `{"type": "file-verified"}` or `{"type": "file-failed", "reason": "<failure-reason>"}`

## 4. Data Channel Protocol

The data channel transfers binary data using a framed format.

### Chunk Format

Each chunk is framed with minimal metadata:
```
[4 bytes sequence number][4 bytes length][data bytes]
```

- **Sequence Number**: 4-byte big-endian integer (0-based index)
- **Length**: 4-byte big-endian integer (size of data in bytes)
- **Data**: Raw binary data of the specified length

## 5. Transfer Protocol

P2PFTP leverages WebRTC's built-in reliability guarantees while implementing a simplified flow control mechanism.

### Sender Process

1. Calculate total chunks based on file size and negotiated chunk size
2. Send file metadata via control channel
3. For each chunk in the window:
   - Send framed chunk data via data channel
   - Track sent chunks for progress reporting
4. Monitor data channel's bufferedAmount to avoid overwhelming the channel:
   - If bufferedAmount exceeds threshold (1MB), pause sending until it decreases
5. Process progress updates from receiver:
   - Adjust sending rate based on receiver's progress
6. Send completion message when all chunks are sent
7. Wait for verification message from receiver

### Receiver Process

1. Receive file metadata and prepare buffer or file handle
2. For each received chunk:
   - Extract sequence number and size from header
   - Write chunk data to file at the correct offset
   - Track progress and periodically send progress updates
3. When all chunks are received:
   - Verify file integrity using MD5 hash
   - Send verification result to sender

## 6. Flow Control

P2PFTP implements a simplified flow control mechanism:

1. **Basic Window Control**:
   - Start with window size of 64 chunks
   - Sender can have up to window_size chunks "in flight"
   - Receiver sends periodic progress updates
   - Sender adjusts sending rate based on progress updates

2. **Congestion Handling**:
   - Monitor data channel's bufferedAmount
   - If bufferedAmount exceeds 1MB, pause sending until it decreases
   - Resume sending when bufferedAmount falls below threshold

## 7. Error Handling

1. **Connection Errors**:
   - Monitor WebRTC connection state
   - If connection is lost, pause transfer
   - Attempt to reconnect through signaling server
   - Resume transfer from last progress point if reconnection succeeds

2. **Transfer Errors**:
   - File integrity is verified with MD5 hash after all chunks are received
   - If verification fails, restart transfer or offer manual retry

## 8. Security Considerations

1. **End-to-End Encryption**: All data is encrypted using WebRTC's built-in DTLS
2. **Token-Based Authentication**: Unique tokens prevent unauthorized connections
3. **No Server Storage**: Files are transferred directly between peers, with no server storage

## 9. Implementation Requirements

To implement a compatible client:

1. Support WebSocket signaling protocol
2. Implement WebRTC with data channel support as specified
3. Support the control channel message formats
4. Implement the data channel framing format
5. Support the simplified flow control mechanism
6. Implement robust error handling
7. Perform end-to-end MD5 verification

## 10. Testing Compatibility

To test compatibility with P2PFTP:

1. Connect to a P2PFTP signaling server
2. Complete the signaling process
3. Establish WebRTC data channels
4. Exchange capabilities (MANDATORY)
5. Transfer a test file in both directions
6. Verify file integrity using MD5 hash