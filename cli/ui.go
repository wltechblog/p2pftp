package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type UI struct {
    client      *Client
    token       string
    lastRequest string // Track the most recent request token

    // TUI components
    app         *tview.Application
    chatView    *tview.TextView
    debugView   *tview.TextView
    fileView    *tview.TextView
    inputField  *tview.InputField
    layout      *tview.Flex
    
    // Channel for async operations
    opChan      chan func()
}

func NewUI(client *Client) *UI {
    ui := &UI{
        client: client,
        app:    tview.NewApplication(),
        opChan: make(chan func(), 100),
    }

    // Start operations handler
    go func() {
        for op := range ui.opChan {
            ui.app.QueueUpdateDraw(op)
        }
    }()

    // Initialize components
    // Create command history
    commandHistory := make([]string, 0, 100)
    historyIndex := -1

    chatView := tview.NewTextView()
    chatView.SetDynamicColors(true)
    chatView.SetScrollable(true)
    chatView.SetTitle("Chat")
    chatView.SetBorder(true)
    chatView.Box.SetTitleAlign(tview.AlignLeft)
    chatView.SetWrap(true)
    chatView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
        switch action {
        case tview.MouseScrollUp:
            row, _ := chatView.GetScrollOffset()
            chatView.ScrollTo(row-1, 0)
            return action, nil
        case tview.MouseScrollDown:
            row, _ := chatView.GetScrollOffset()
            chatView.ScrollTo(row+1, 0)
            return action, nil
        }
        return action, event
    })
    ui.chatView = chatView

    debugView := tview.NewTextView()
    debugView.SetDynamicColors(true)
    debugView.SetScrollable(true)
    debugView.SetTitle("Debug")
    debugView.SetBorder(true)
    debugView.Box.SetTitleAlign(tview.AlignLeft)
    debugView.SetWrap(true)
    debugView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
        switch action {
        case tview.MouseScrollUp:
            row, _ := debugView.GetScrollOffset()
            debugView.ScrollTo(row-1, 0)
            return action, nil
        case tview.MouseScrollDown:
            row, _ := debugView.GetScrollOffset()
            debugView.ScrollTo(row+1, 0)
            return action, nil
        }
        return action, event
    })
    ui.debugView = debugView

    fileView := tview.NewTextView()
    fileView.SetDynamicColors(true)
    fileView.SetScrollable(false)
    fileView.SetTitle("Transfer Status")
    fileView.SetBorder(true)
    fileView.Box.SetTitleAlign(tview.AlignLeft)
    fileView.SetWrap(false)
    fileView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
        switch action {
        case tview.MouseScrollUp:
            row, _ := fileView.GetScrollOffset()
            fileView.ScrollTo(row-1, 0)
            return action, nil
        case tview.MouseScrollDown:
            row, _ := fileView.GetScrollOffset()
            fileView.ScrollTo(row+1, 0)
            return action, nil
        }
        return action, event
    })
    ui.fileView = fileView

    inputField := tview.NewInputField()
    inputField.SetLabel("> ")
    inputField.SetFieldWidth(0)
    inputField.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
        switch event.Key() {
        case tcell.KeyUp:
            if historyIndex < len(commandHistory)-1 {
                historyIndex++
                inputField.SetText(commandHistory[len(commandHistory)-1-historyIndex])
            }
            return nil
        case tcell.KeyDown:
            if historyIndex > 0 {
                historyIndex--
                inputField.SetText(commandHistory[len(commandHistory)-1-historyIndex])
            } else if historyIndex == 0 {
                historyIndex = -1
                inputField.SetText("")
            }
            return nil
        }
        return event
    })
    ui.inputField = inputField

    // Set up tab completion for file paths
    ui.inputField.SetAutocompleteFunc(ui.FileAutocomplete)

    // Set up input handling with history
    ui.inputField.SetDoneFunc(func(key tcell.Key) {
        text := ui.inputField.GetText()
        if text != "" && (len(commandHistory) == 0 || commandHistory[len(commandHistory)-1] != text) {
            commandHistory = append(commandHistory, text)
            if len(commandHistory) > 100 {
                commandHistory = commandHistory[1:]
            }
        }
        historyIndex = -1
        ui.HandleInput(key)
    })

    // Create layout with resizable panels
    topFlex := tview.NewFlex().
        AddItem(ui.chatView, 0, 2, false).
        AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
            AddItem(ui.debugView, 0, 1, false).
            AddItem(ui.fileView, 0, 1, false), 0, 1, false)

    ui.layout = tview.NewFlex().SetDirection(tview.FlexRow).
        AddItem(topFlex, 0, 1, false).
        AddItem(ui.inputField, 1, 0, true)

    // Make panels resizable
    ui.app.EnableMouse(true)

    // Add help text to chat view
    fmt.Fprintf(ui.chatView, "Commands:\n")
    fmt.Fprintf(ui.chatView, "  /token - Show your token (click on token to copy)\n")
    fmt.Fprintf(ui.chatView, "  /connect <token> - Connect to a peer\n")
    fmt.Fprintf(ui.chatView, "  /accept [token] - Accept connection request\n")
    fmt.Fprintf(ui.chatView, "  /reject [token] - Reject connection request\n")
    fmt.Fprintf(ui.chatView, "  /send <path> - Send a file (press Tab for completion)\n")
    fmt.Fprintf(ui.chatView, "  /quit - Exit program\n\n")
    fmt.Fprintf(ui.chatView, "Type any message to send chat (without / prefix)\n\n")

    return ui
}

func (ui *UI) FileAutocomplete(currentText string) []string {
    if !strings.HasPrefix(currentText, "/send ") {
        return nil
    }

    path := strings.TrimPrefix(currentText, "/send ")
    if path == "" {
        path = "."
    }

    dir := filepath.Dir(path)
    base := filepath.Base(path)

    entries, err := os.ReadDir(dir)
    if err != nil {
        return nil
    }

    var matches []string
    for _, entry := range entries {
        name := entry.Name()
        if strings.HasPrefix(name, base) {
            fullPath := filepath.Join(dir, name)
            if entry.IsDir() {
                fullPath += "/"
            }
            matches = append(matches, "/send "+fullPath)
        }
    }

    return matches
}

func (ui *UI) HandleInput(key tcell.Key) {
    text := ui.inputField.GetText()
    if text == "" {
        return
    }

    // Clear input field
    ui.inputField.SetText("")
    ui.app.SetFocus(ui.inputField)

    // Handle input
    text = strings.TrimSpace(text)
    if !strings.HasPrefix(text, "/") {
        // Queue chat message sending
        ui.ShowChat(ui.token, text)
        go func(msg string) {
            if err := ui.client.SendChat(msg); err != nil {
                ui.ShowError(fmt.Sprintf("Error sending message: %v", err))
            }
        }(text)
        return
    }

    parts := strings.Fields(text)
    if len(parts) == 0 {
        return
    }

    cmd := parts[0]
    switch cmd {
    case "/quit":
        close(ui.opChan)
        ui.app.Stop()
        os.Exit(0)

    case "/token":
        if ui.token == "" {
            ui.ShowError("Token not yet received. Please wait...")
        } else {
            ui.AppendChat(fmt.Sprintf("[::b]Your token[white] (click to copy):\n[blue]%s[::-]", ui.token))
        }

    case "/connect":
        if len(parts) != 2 {
            ui.ShowError("Usage: /connect <token>")
            return
        }
        if ui.token == "" {
            ui.ShowError("Please wait for your token before connecting")
            return
        }
        ui.LogDebug("Connecting to peer...")
        go func(token string) {
            if err := ui.client.Connect(token); err != nil {
                ui.ShowError(fmt.Sprintf("Error connecting: %v", err))
            }
        }(parts[1])

    case "/accept":
        if ui.token == "" {
            ui.ShowError("Please wait for your token before accepting")
            return
        }
        var tokenToAccept string
        if len(parts) > 1 {
            tokenToAccept = parts[1]
        } else if ui.lastRequest != "" {
            tokenToAccept = ui.lastRequest
        } else {
            ui.ShowError("No pending request to accept")
            return
        }
        ui.LogDebug("Accepting connection request...")
        toAccept := tokenToAccept
        lastReq := ui.lastRequest
        ui.lastRequest = ""
        go func() {
            if err := ui.client.Accept(toAccept); err != nil {
                ui.ShowError(fmt.Sprintf("Error accepting: %v", err))
                ui.lastRequest = lastReq
            }
        }()

    case "/reject":
        if ui.token == "" {
            ui.ShowError("Please wait for your token before rejecting")
            return
        }
        var tokenToReject string
        if len(parts) > 1 {
            tokenToReject = parts[1]
        } else if ui.lastRequest != "" {
            tokenToReject = ui.lastRequest
        } else {
            ui.ShowError("No pending request to reject")
            return
        }
        ui.LogDebug("Rejecting connection request...")
        toReject := tokenToReject
        lastReq := ui.lastRequest
        ui.lastRequest = ""
        go func() {
            if err := ui.client.Reject(toReject); err != nil {
                ui.ShowError(fmt.Sprintf("Error rejecting: %v", err))
                ui.lastRequest = lastReq
            }
        }()

    case "/send":
        if len(parts) != 2 {
            ui.ShowError("Usage: /send <path>")
            return
        }
        ui.LogDebug("Starting file transfer...")
        go func(path string) {
            if err := ui.client.SendFile(path); err != nil {
                ui.ShowError(fmt.Sprintf("Error sending file: %v", err))
            }
        }(parts[1])

    default:
        ui.ShowError(fmt.Sprintf("Unknown command: %s", cmd))
    }
}

func (ui *UI) Run() error {
    defer close(ui.opChan)
    return ui.app.SetRoot(ui.layout, true).Run()
}

func copyToClipboard(text string) error {
    // Try xclip first (X11)
    if _, err := exec.LookPath("xclip"); err == nil {
        cmd := exec.Command("xclip", "-selection", "clipboard")
        cmd.Stdin = strings.NewReader(text)
        return cmd.Run()
    }

    // Try pbcopy (macOS)
    if _, err := exec.LookPath("pbcopy"); err == nil {
        cmd := exec.Command("pbcopy")
        cmd.Stdin = strings.NewReader(text)
        return cmd.Run()
    }

    return fmt.Errorf("no clipboard command available")
}

func (ui *UI) SetToken(token string) {
    ui.token = token

    // Create a clickable token message
    tokenMsg := fmt.Sprintf("Token received: [::u]%s[::-]", token)
    
    // Add click handler for the token
    ui.chatView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
        if action == tview.MouseLeftClick {
            x, y := event.Position()
            text := ui.chatView.GetText(false)
            lines := strings.Split(text, "\n")
            
            // Find the token line
            for i, line := range lines {
                if strings.Contains(line, token) && y == i {
                    // Check if click is within token bounds
                    tokenStart := strings.Index(line, token)
                    tokenEnd := tokenStart + len(token)
                    if x >= tokenStart && x < tokenEnd {
                        go func() {
                            if err := copyToClipboard(token); err != nil {
                                ui.ShowError(fmt.Sprintf("Failed to copy token: %v", err))
                            } else {
                                ui.AppendChat("[green]Token copied to clipboard![white]")
                            }
                        }()
                        return action, nil
                    }
                }
            }
        }
        
        // Handle scrolling
        switch action {
        case tview.MouseScrollUp:
            row, _ := ui.chatView.GetScrollOffset()
            ui.chatView.ScrollTo(row-1, 0)
            return action, nil
        case tview.MouseScrollDown:
            row, _ := ui.chatView.GetScrollOffset()
            ui.chatView.ScrollTo(row+1, 0)
            return action, nil
        }
        
        return action, event
    })

    ui.AppendChat(tokenMsg)
}

func (ui *UI) ShowConnectionRequest(token string) {
    ui.lastRequest = token
    ui.AppendChat("[yellow]Peer[white] wants to connect (use [blue]/accept[white] to connect)")
    ui.app.SetFocus(ui.inputField)
}

func (ui *UI) ShowConnectionAccepted(msg string) {
    ui.AppendChat("[green]✓ Connected to Peer[white]")
    ui.app.SetFocus(ui.inputField)
}

func (ui *UI) ShowConnectionRejected(token string) {
    ui.AppendChat("[red]× Connection rejected by Peer[white]")
    ui.app.SetFocus(ui.inputField)
}

func (ui *UI) ShowError(msg string) {
    ui.opChan <- func() {
        timestamp := time.Now().Format("15:04:05")
        fmt.Fprintf(ui.debugView, "[gray]%s [red]Error:[white] %s\n", timestamp, msg)
        ui.debugView.ScrollToEnd()
    }
}

func (ui *UI) LogDebug(msg string) {
    ui.opChan <- func() {
        timestamp := time.Now().Format("15:04:05")
        fmt.Fprintf(ui.debugView, "[gray]%s[white] %s\n", timestamp, msg)
        ui.debugView.ScrollToEnd()
    }
}

func (ui *UI) ShowChat(from string, msg string) {
    if from == ui.token {
        ui.AppendChat(fmt.Sprintf("[yellow]You[white] %s", msg))
    } else {
        ui.AppendChat(fmt.Sprintf("[yellow]Peer[white] %s", msg))
    }
}

// UpdateTransferStatus shows current transfer status in a single line
func (ui *UI) UpdateTransferStatus(status string) {
    ui.opChan <- func() {
        ui.fileView.Clear()
        if status != "" {
            fmt.Fprintf(ui.fileView, "%s", status)
        }
    }
}

// ShowFileTransfer for backward compatibility
func (ui *UI) ShowFileTransfer(msg string) {
    if strings.Contains(msg, "Ready for") || strings.Contains(msg, "Validating") || strings.Contains(msg, "Saved") {
        ui.UpdateTransferStatus(msg)
    }
}

func (ui *UI) AppendChat(msg string) {
    ui.opChan <- func() {
        timestamp := time.Now().Format("15:04:05")
        fmt.Fprintf(ui.chatView, "[gray]%s[white] %s\n", timestamp, msg)
        ui.chatView.ScrollToEnd()
    }
}
