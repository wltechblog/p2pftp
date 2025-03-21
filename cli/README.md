# P2PFTP CLI

This directory contains the command-line interface (CLI) for the P2PFTP application.

## File Structure

The CLI code is organized into the following files:

- **main.go**: Entry point for the CLI application, handles initialization and startup
- **client.go**: Core client functionality and basic operations
- **types.go**: Data structures and constants used throughout the application
- **ui.go**: Terminal user interface implementation using tview
- **webrtc.go**: WebRTC connection handling and setup
- **transfer.go**: File transfer functionality including chunking and sliding window implementation
- **messaging.go**: Chat and messaging functionality

## Building

To build the CLI executable:

```bash
cd cli
go build -o p2pftp-cli
```

## Usage

Run the CLI with:

```bash
./p2pftp-cli -addr example.com:443
```

Where:
- `-addr`: The address of the P2PFTP server (default: localhost:8089)

Note: The CLI always uses secure WebSocket connections (WSS) as the server is expected to be behind an SSL proxy.

## Commands

Once the CLI is running, you can use the following commands:

- `/token` - Show your token (click on token to copy)
- `/connect <token>` - Connect to a peer
- `/accept [token]` - Accept connection request
- `/reject [token]` - Reject connection request
- `/send <path>` - Send a file (press Tab to cycle options)
- `/quit` - Exit program

Type any message without a / prefix to send chat messages.