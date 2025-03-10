# p2pftp

A simple peer-to-peer file transfer tool with a Go backend.

## Features

- Peer-to-peer file transfer over WebRTC data channels
- Secure token-based authentication
- Text chat between peers
- Mobile-first responsive design (NOTE: Android browsers tend to kill webrtc connections when you switch away, so may not be reliable there!)
- Real-time transfer progress indication
- Direct end-to-end encrypted, peer-to-peer communication (no server involvement once connected)

## What it isn't

- A fully anonymous tool, unless you're using through a vpn, tor, etc. webrtc signaling reveals the public IPs of both endpoints.
- A robust chat app with emojis and such
- Cheese

## Requirements

- Go 1.24.1 or higher
- Modern web browsers with WebRTC support
- https reverse proxy such as Caddy or nginx

## Installation

```
go install github.com/wltechblog/p2pftp@latest
```

## Usage

1. Run the Go executable to start the server:
   ```
   p2pftp
   ```

   Optional command line arguments:
   - `-addr`: Listen address (default: localhost)
   - `-port`: Listen port (default: 8089)

   Example with custom address and port:
   ```
   p2pftp -addr 0.0.0.0 -port 9000
   ```

2. Use a https proxy such as Caddy, nginx, etc to provide a secure connection.

3. Open your browser to your URL, share your token with the person you want to connect with, or use the "Copy Link" to give them a URL that directly connects to you.

4. When they attempt to connect, authorize the connection request to hook up.

5. Once the connection is established, you can start chatting and sending files

6. When done, close the tab and your session is gone forever.

## How It Works

1. When users load the page, they establish a WebSocket connection to the server
2. Each user is assigned a unique, secure token
3. To connect, one user enters the other's token and initiates the connection
4. The receiving user is notified and can accept or reject the connection, and can validate the peer token
5. When accepted, WebRTC signaling occurs through the server
6. After the WebRTC connection is established, all communication happens directly between peers
7. No file data passes through the server, ensuring privacy and reducing server load

## Development

The project structure is simple:
- `main.go`: Go server implementation
- `static/index.html`: Frontend HTML
- `static/app.js`: Frontend JavaScript

To build the executable:
```
go build -o p2pftp main.go
```

## License

[MIT License](LICENSE)