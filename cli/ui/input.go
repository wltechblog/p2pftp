package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// handleInput processes user input
func (ui *UI) handleInput(text string) {
	if text == "" {
		return
	}

	// Check if it's a command
	if strings.HasPrefix(text, "/") {
		ui.handleCommand(text)
	} else {
		// It's a chat message
		ui.handleChatMessage(text)
	}
}

// handleCommand processes a command
func (ui *UI) handleCommand(text string) {
	// Split the command and arguments
	parts := strings.SplitN(text, " ", 2)
	command := parts[0]
	var args string
	if len(parts) > 1 {
		args = parts[1]
	}

	// Process the command
	switch command {
	case "/help":
		ui.showHelp()
	case "/token":
		ui.showToken()
	case "/connect":
		ui.connectToPeer(args)
	case "/accept":
		ui.acceptConnection(args)
	case "/reject":
		ui.rejectConnection(args)
	case "/send":
		ui.sendFile(args)
	case "/quit":
		ui.app.Stop()
	default:
		ui.ShowError(fmt.Sprintf("Unknown command: %s", command))
	}
}

// handleChatMessage sends a chat message
func (ui *UI) handleChatMessage(text string) {
	err := ui.client.SendChat(text)
	if err != nil {
		ui.ShowError(fmt.Sprintf("Failed to send message: %v", err))
		return
	}

	// Show the message in the chat view
	ui.ShowChat(ui.token, text)
}

// showHelp displays the help message
func (ui *UI) showHelp() {
	ui.AppendChat("[green]Available commands:[white]")
	ui.AppendChat("  [blue]/token[white] - Show your token (click on token to copy)")
	ui.AppendChat("  [blue]/connect <token>[white] - Connect to a peer")
	ui.AppendChat("  [blue]/accept [token][white] - Accept connection request")
	ui.AppendChat("  [blue]/reject [token][white] - Reject connection request")
	ui.AppendChat("  [blue]/send <path>[white] - Send a file (press Tab to cycle options)")
	ui.AppendChat("  [blue]/quit[white] - Exit program")
	ui.AppendChat("Type any message without a / prefix to send chat messages.")
}

// showToken displays the user's token
func (ui *UI) showToken() {
	if ui.token == "" {
		ui.ShowError("Token not yet received from server")
		return
	}

	ui.AppendChat(fmt.Sprintf("Your token: [::u]%s[::-]", ui.token))
}

// connectToPeer connects to a peer
func (ui *UI) connectToPeer(token string) {
	if token == "" {
		ui.ShowError("Please provide a token to connect to")
		return
	}

	err := ui.client.Connect(token)
	if err != nil {
		ui.ShowError(fmt.Sprintf("Failed to connect: %v", err))
		return
	}

	ui.AppendChat(fmt.Sprintf("Connecting to peer with token: %s", token))
}

// acceptConnection accepts a connection request
func (ui *UI) acceptConnection(token string) {
	// If no token is provided, use the last request
	if token == "" {
		if ui.lastRequest == "" {
			ui.ShowError("No pending connection request")
			return
		}
		token = ui.lastRequest
	}

	err := ui.client.Accept(token)
	if err != nil {
		ui.ShowError(fmt.Sprintf("Failed to accept connection: %v", err))
		return
	}

	ui.AppendChat(fmt.Sprintf("Accepting connection from peer with token: %s", token))
}

// rejectConnection rejects a connection request
func (ui *UI) rejectConnection(token string) {
	// If no token is provided, use the last request
	if token == "" {
		if ui.lastRequest == "" {
			ui.ShowError("No pending connection request")
			return
		}
		token = ui.lastRequest
	}

	err := ui.client.Reject(token)
	if err != nil {
		ui.ShowError(fmt.Sprintf("Failed to reject connection: %v", err))
		return
	}

	ui.AppendChat(fmt.Sprintf("Rejecting connection from peer with token: %s", token))
}

// sendFile sends a file to the peer
func (ui *UI) sendFile(path string) {
	if path == "" {
		ui.ShowError("Please provide a file path to send")
		return
	}

	// Check if the file exists
	_, err := os.Stat(path)
	if err != nil {
		ui.ShowError(fmt.Sprintf("File not found: %s", path))
		return
	}

	// Send the file in a goroutine to avoid blocking the UI
	go func() {
		err := ui.client.SendFile(path)
		if err != nil {
			ui.ShowError(fmt.Sprintf("Failed to send file: %v", err))
			return
		}
	}()

	ui.AppendChat(fmt.Sprintf("Sending file: %s", path))
}

// SetToken sets the user's token
func (ui *UI) SetToken(token string) {
	ui.token = token

	// Create a clickable token message
	tokenMsg := fmt.Sprintf("Token received: [::u]%s[::-]", token)
	
	ui.AppendChat(tokenMsg)
}

// ShowConnectionRequest displays a connection request
func (ui *UI) ShowConnectionRequest(token string) {
	ui.lastRequest = token
	ui.AppendChat("[yellow]Peer[white] wants to connect (use [blue]/accept[white] to connect)")
	ui.app.SetFocus(ui.inputField)
}

// ShowConnectionAccepted displays a connection accepted message
func (ui *UI) ShowConnectionAccepted(msg string) {
	ui.AppendChat("[green]✓ Connected to Peer[white]")
	ui.app.SetFocus(ui.inputField)
}

// ShowConnectionRejected displays a connection rejected message
func (ui *UI) ShowConnectionRejected(token string) {
	ui.AppendChat("[red]× Connection rejected by Peer[white]")
	ui.app.SetFocus(ui.inputField)
}

// copyToClipboard copies text to the clipboard
func copyToClipboard(text string) error {
	// Try different clipboard commands based on the platform
	commands := []struct {
		cmd  string
		args []string
	}{
		{"xclip", []string{"-selection", "clipboard"}},
		{"xsel", []string{"-b"}},
		{"pbcopy", []string{}},
		{"clip", []string{}},
	}

	for _, cmd := range commands {
		copyCmd := exec.Command(cmd.cmd, cmd.args...)
		in, err := copyCmd.StdinPipe()
		if err != nil {
			continue
		}

		if err := copyCmd.Start(); err != nil {
			continue
		}

		if _, err := in.Write([]byte(text)); err != nil {
			continue
		}

		if err := in.Close(); err != nil {
			continue
		}

		return copyCmd.Wait()
	}

	return fmt.Errorf("no clipboard command available")
}