# P2PFTP Development Guidelines

## Build and Run Commands
- Build: `go build` or `go build -o p2pftp main.go`
- Run server: `go run main.go` or `p2pftp -addr 0.0.0.0 -port 8089`
- Run CLI client: `go run cli/main.go cli/ui.go` or `cd cli && go run main.go ui.go`
- Lint: `go vet ./...`
- Format: `go fmt ./...`
- Install: `go install github.com/wltechblog/p2pftp@latest`

## Code Style Guidelines
- **Imports**: Standard library first, third-party packages second, grouped by empty line
- **Error Handling**: Check errors immediately, use descriptive error messages
- **Naming**: CamelCase for exported identifiers, camelCase for non-exported, ALL_CAPS for constants
- **WebRTC**: Properly validate connection and data channel states before operations
- **File Transfer**: Use chunked approach (64KB chunks) with sequence tracking
- **JavaScript**: Keep UI logic separate from WebRTC and file transfer logic
- **Documentation**: Add comments for exported functions and complex logic
- **Logging**: Use appropriate logging for debugging and user feedback

## Project Organization
- `main.go`: Server implementation for signaling
- `cli/`: Command-line client implementation
- `static/`: Web frontend (HTML, JS, CSS)
- `embed.go`: Embeds static files into binary