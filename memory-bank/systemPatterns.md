# System Architecture Patterns

## Core Subsystems
```mermaid
flowchart LR
    Browser[[Browser UI]] <-->|WebSocket| Signal[Signaling Service]
    Signal <-->|gRPC| Client[Native Client]
    Client <-->|WebRTC| Browser
    Client <-->|Chunk Stream| Transfer[File Transfer]

classDef service fill:#bbf,stroke:#339;
classDef component fill:#fbf,stroke:#909;
class Browser,Client component
class Signal,Transfer service
```

## Key Flows
1. **Signaling Initiation**
   - Browser establishes WebSocket connection
   - SDP offer/answer exchange
   - ICE candidate negotiation

2. **File Transfer**
   - Chunk segmentation (256KB blocks)
   - Checksum verification
   - Resume capability via offset tracking
