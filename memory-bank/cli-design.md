# CLI Client Design

## Overview

The new CLI client will be designed with simplicity and robustness in mind, avoiding the deadlocks and race conditions that plagued the previous implementation. It will follow an IRC-like interface with a readline-like input mechanism and will strictly adhere to the updated protocol specification.

## Design Goals

1. **Simplicity**: Keep the design simple and focused on core functionality
2. **Robustness**: Avoid race conditions and deadlocks
3. **Protocol Compliance**: Strictly follow the updated protocol specification
4. **Separation of Concerns**: Clear separation between UI, networking, and file transfer logic
5. **Error Handling**: Comprehensive error handling and user feedback

## Architecture

The CLI client will be structured into the following components:

```
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│                 │      │                 │      │                 │
│   UI Manager    │<─────│  Core Manager   │<─────│  Network Layer  │
│                 │      │                 │      │                 │
└─────────────────┘      └─────────────────┘      └─────────────────┘
        │                        │                        │
        │                        │                        │
        ▼                        ▼                        ▼
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│                 │      │                 │      │                 │
│  Input Handler  │      │ Transfer Manager│      │ WebRTC Manager  │
│                 │      │                 │      │                 │
└─────────────────┘      └─────────────────┘      └─────────────────┘
```

### Components

1. **UI Manager**
   - Handles the display of messages, progress bars, and status information
   - Manages the screen layout and updates
   - Uses a simple, non-blocking approach without complex TUI libraries

2. **Input Handler**
   - Processes user input commands
   - Implements readline-like functionality for command history and editing
   - Runs in a separate goroutine to avoid blocking the UI

3. **Core Manager**
   - Central coordinator that connects all components
   - Manages application state
   - Routes messages between components

4. **Transfer Manager**
   - Handles file transfer operations
   - Implements the file transfer protocol
   - Manages progress tracking and reporting

5. **Network Layer**
   - Handles WebSocket communication with the signaling server
   - Processes signaling messages
   - Manages connection state

6. **WebRTC Manager**
   - Establishes and maintains WebRTC connections
   - Creates and manages data channels
   - Implements the WebRTC-specific parts of the protocol

## Communication Flow

Components will communicate using Go channels to avoid race conditions and deadlocks:

```
type Message struct {
    Type    MessageType
    Payload interface{}
}

type MessageType int

const (
    UIUpdate MessageType = iota
    UserCommand
    NetworkEvent
    TransferUpdate
    WebRTCEvent
    // ...
)
```

Each component will have dedicated input and output channels, with the Core Manager acting as the central router.

## UI Design

The UI will be simple and text-based, similar to IRC clients:

```
+--------------------------------------------------------------+
| [System] Connected to server                                 |
| [System] Your token: abc12345                                |
| [System] Type /help for available commands                   |
| [User] /connect xyz67890                                     |
| [System] Connecting to peer xyz67890...                      |
| [System] Connection established                              |
| [xyz67890] Hello!                                            |
| [User] Hi there                                              |
| [System] Receiving file: example.txt (1.2 MB)                |
| [System] Transfer progress: [====================] 100%      |
| [System] File received and verified                          |
+--------------------------------------------------------------+
| > _                                                          |
+--------------------------------------------------------------+
```

## Error Handling

Error handling will be comprehensive and user-friendly:

1. **Network Errors**:
   - Automatic reconnection attempts with backoff
   - Clear error messages to the user
   - Status indicators for connection state

2. **Transfer Errors**:
   - Detailed error reporting for failed transfers
   - Option to retry failed transfers
   - Verification failures reported with specific reasons

3. **User Input Errors**:
   - Validation of commands before execution
   - Helpful error messages for invalid commands
   - Command help and suggestions

## Implementation Approach

1. **Start Simple**:
   - Begin with a minimal implementation that can connect and chat
   - Add file transfer capabilities incrementally
   - Test thoroughly at each step

2. **Avoid Blocking Operations**:
   - Use goroutines for potentially blocking operations
   - Implement timeouts for all network operations
   - Use channels for communication between components

3. **Testing Strategy**:
   - Unit tests for each component
   - Integration tests for component interactions
   - End-to-end tests for full functionality

## Command Set

The CLI will support the following commands:

1. `/connect <token>` - Connect to a peer
2. `/accept <token>` - Accept a connection request
3. `/reject <token>` - Reject a connection request
4. `/send <filepath>` - Send a file
5. `/disconnect` - Disconnect from the current peer
6. `/quit` - Exit the application
7. `/help` - Show available commands
8. `/status` - Show connection status

Any text not starting with `/` will be sent as a chat message.

## Next Steps

1. Implement the basic UI framework
2. Implement the WebSocket connection to the signaling server
3. Implement the WebRTC connection establishment
4. Implement the chat functionality
5. Implement the file transfer functionality
6. Add error handling and recovery mechanisms
7. Test thoroughly against the browser client