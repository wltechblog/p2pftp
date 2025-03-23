# Product Context

## Problem Space
- Enables direct device-to-device transfers without cloud storage
- Solves large file sharing challenges in restricted network environments
- Bridges browser-initiated transfers with native client capabilities

## Key User Journeys
```mermaid
sequenceDiagram
    Participant B as Browser
    Participant S as Signaling Server
    Participant C as Native Client
    
    B->>S: Initiate transfer session
    S->>C: Forward connection request
    C->>S: Accept with SDP offer
    S->>B: Relay signaling data
    B->>C: Establish WebRTC connection
    C->>B: Stream file chunks
```

## Success Metrics
- 90%+ successful NAT traversal
- Sub-5 second connection establishment
- >100MB/s transfer speeds on LAN
