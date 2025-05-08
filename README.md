# p2pftp

A service providing secure and reliable P2P file transfers and chat

## Features
- Command-line interface for file transfers and chat
- Peer-to-peer file transfer over WebRTC data channels
- Secure token-based authentication
- Text chat between peers
- Direct end-to-end encrypted, peer-to-peer communication (no server involvement once connected)
- Works through typical NAT situations
- Robust error handling with automatic retransmission of missing chunks

## What it isn't

- A fully anonymous tool, unless you're using through a vpn, tor, etc that makes all your traffic anonymous. webrtc signaling reveals the public IPs of both endpoints, and webrtc support over VPN is not ubiquitous.
- A robust chat app with emojis and such
- Gluten free
- Able to deal with complex, multi-layered NAT or VPNs that don't play well with WebRTC

## Motivations

Email is a terrible way to send files, and has small size limits. File storage products like Dropbox or Google Drive
are great if you're sharing one file to many people, but not if you're sharing one file to one person. Cutting out the
middle-man and sending data directly to the intended recipient is ideal in many cases, and doing so with full end-to-end
encryption is a must.

## Requirements

- Go 1.24.1 or higher
- https reverse proxy such as Caddy or nginx for production deployments

## Installation

```
go install github.com/wltechblog/p2pftp@latest
```

## Usage

### Server

1. Run the Go executable to start the signaling server:
   ```
   p2pftp-server
   ```

   Optional command line arguments:
   - `-addr`: Listen address (default: localhost)
   - `-port`: Listen port (default: 8089)

   Example with custom address and port:
   ```
   p2pftp-server -addr 0.0.0.0 -port 9000
   ```

2. For production use, set up a https proxy such as Caddy, nginx, etc to provide a secure connection. Forward all requests for the URL hostname to localhost:8089 or whatever you specify in your command line.

   **Note**: The CLI client always uses secure WebSocket connections (WSS) as it expects the server to be behind an SSL proxy.

### Client

1. Run the CLI client:
   ```
   p2pftp-cli
   ```

   Optional command line arguments:
   - `-server`: Signaling server hostname (default: p2p.teamworkapps.com)
   - `-url`: Full connection URL (e.g., https://p2p.teamworkapps.com/?token=abcd1234)
   - `-debug`: Enable debug logging
   - `-logfile`: Path to debug log file (default: p2pftp-debug.log)

2. Follow the on-screen instructions to connect to a peer, send/receive files, and chat.

## How It Works

1. When users start the client, they establish a WebSocket connection to the signaling server
2. Each user is assigned a unique, secure token
3. To connect, one user enters the other's token and initiates the connection
4. The receiving user is notified and can accept or reject the connection
5. When accepted, WebRTC signaling occurs through the server
6. After the WebRTC connection is established, all communication happens directly between peers
7. No file data passes through the server, ensuring privacy and reducing server load

## Development

The project structure is simple:
- `main.go`: Signaling server implementation
- `client/`: CLI client implementation
  - `main.go`: Main client code
  - `webrtc/`: WebRTC implementation
  - `transfer/`: File transfer implementation

To build the executables:
```
make build
```

This will create:
- `bin/p2pftp-server`: The signaling server
- `bin/p2pftp-cli`: The CLI client

## License

[MIT License](LICENSE)