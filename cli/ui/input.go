package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// processCommand handles user commands
func (u *UI) processCommand(cmd string) bool {
    parts := strings.Fields(cmd)
    if len(parts) == 0 {
        return true
    }

    command := strings.ToLower(parts[0])
    args := parts[1:]

    switch command {
    case "/help":
        u.showHelp()

    case "/connect":
        if len(args) != 1 {
            u.ShowError("Usage: /connect <token>")
            return true
        }
        if err := u.client.Connect(args[0]); err != nil {
            u.ShowError(err.Error())
        }

    case "/accept":
        token := ""
        if len(args) > 0 {
            token = args[0]
        }
        if err := u.client.Accept(token); err != nil {
            u.ShowError(err.Error())
        }

    case "/reject":
        if len(args) != 1 {
            u.ShowError("Usage: /reject <token>")
            return true
        }
        if err := u.client.Disconnect(); err != nil {
            u.ShowError(err.Error())
        }

    case "/send":
        if len(args) != 1 {
            u.ShowError("Usage: /send <filepath>")
            return true
        }
        if err := u.client.SendFile(args[0]); err != nil {
            u.ShowError(err.Error())
        }

    case "/quit", "/exit":
        return false

    case "/disconnect":
        if err := u.client.Disconnect(); err != nil {
            u.ShowError(err.Error())
        }

    default:
        // If command doesn't start with /, treat it as a chat message
        if !strings.HasPrefix(command, "/") {
            msg := cmd
            if err := u.client.SendChat(msg); err != nil {
                u.ShowError(err.Error())
            }
        } else {
            u.ShowError(fmt.Sprintf("Unknown command: %s\nType /help for available commands", command))
        }
    }

    return true
}

func (u *UI) showHelp() {
    fmt.Println("\nAvailable commands:")
    fmt.Println("  /help             - Show this help message")
    fmt.Println("  /connect <token>  - Connect to a peer")
    fmt.Println("  /accept [token]   - Accept connection request (recent request if no token)")
    fmt.Println("  /reject <token>   - Reject connection request")
    fmt.Println("  /send <filepath>  - Send a file to connected peer")
    fmt.Println("  /disconnect       - Disconnect from current peer")
    fmt.Println("  /quit or /exit    - Exit the application")
    fmt.Println("\nAny text not starting with / will be sent as chat message")
}

// Run starts the UI and processes user input
func (u *UI) Run() error {
    fmt.Println("P2P File Transfer CLI")
    fmt.Println("Type /help for list of commands")

    fmt.Print("> ")
    scanner := bufio.NewScanner(os.Stdin)
    for scanner.Scan() {
        input := scanner.Text()
        if u.debugEnabled {
            u.LogDebug(fmt.Sprintf("Command: %s", input))
        }
        
        if !u.processCommand(input) {
            u.ShowInfo("Exiting...")
            break
        }
        fmt.Print("\n> ")
    }

    if err := scanner.Err(); err != nil {
        return fmt.Errorf("error reading input: %v", err)
    }

    return nil
}
