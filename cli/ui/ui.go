package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Client interface for client operations
type Client interface {
	Connect(peerToken string) error
	Accept(peerToken string) error
	Reject(peerToken string) error
	SendChat(text string) error
	SendFile(path string) error
	Disconnect() error
}

// UI represents the user interface
type UI struct {
	app           *tview.Application
	client        Client
	chatView      *tview.TextView
	debugView     *tview.TextView
	fileView      *tview.TextView
	inputField    *tview.InputField
	flex          *tview.Flex
	token         string
	lastRequest   string
	transfers     []transfer
	opChan        chan func()
}

// transfer represents a file transfer
type transfer struct {
	status    string
	timestamp time.Time
}

// NewUI creates a new UI
func NewUI(client Client) *UI {
	ui := &UI{
		app:       tview.NewApplication(),
		client:    client,
		transfers: make([]transfer, 0),
		opChan:    make(chan func(), 100),
	}

	// Create UI components
	ui.createComponents()

	// Start operation handler
	go ui.handleOperations()

	return ui
}

// createComponents creates the UI components
func (ui *UI) createComponents() {
	// Create chat view
	ui.chatView = tview.NewTextView().
		SetDynamicColors(true).
		SetChangedFunc(func() {
			ui.app.Draw()
		})
	ui.chatView.SetBorder(true).SetTitle("Chat")

	// Create debug view
	ui.debugView = tview.NewTextView().
		SetDynamicColors(true).
		SetChangedFunc(func() {
			ui.app.Draw()
		})
	ui.debugView.SetBorder(true).SetTitle("Debug")

	// Create file view
	ui.fileView = tview.NewTextView().
		SetDynamicColors(true).
		SetChangedFunc(func() {
			ui.app.Draw()
		})
	ui.fileView.SetBorder(true).SetTitle("Transfers")

	// Create input field
	ui.inputField = tview.NewInputField().
		SetLabel("> ").
		SetFieldWidth(0).
		SetDoneFunc(func(key tcell.Key) {
			if key == tcell.KeyEnter {
				ui.handleInput(ui.inputField.GetText())
				ui.inputField.SetText("")
			}
		})

	// Create layout
	ui.flex = tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(tview.NewFlex().
			AddItem(ui.chatView, 0, 2, false).
			AddItem(tview.NewFlex().
				SetDirection(tview.FlexRow).
				AddItem(ui.fileView, 0, 1, false).
				AddItem(ui.debugView, 0, 1, false),
				0, 1, false),
			0, 1, false).
		AddItem(ui.inputField, 1, 0, true)

	// Set up key bindings
	ui.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Global key bindings
		switch event.Key() {
		case tcell.KeyCtrlC:
			ui.app.Stop()
			return nil
		case tcell.KeyTab:
			// Cycle focus between input field and chat view
			if ui.app.GetFocus() == ui.inputField {
				ui.app.SetFocus(ui.chatView)
			} else {
				ui.app.SetFocus(ui.inputField)
			}
			return nil
		}
		return event
	})
}

// Run starts the UI
func (ui *UI) Run() error {
	ui.app.SetRoot(ui.flex, true)
	
	// Display help message after a short delay to ensure UI is ready
	go func() {
		time.Sleep(100 * time.Millisecond)
		ui.showHelp()
	}()
	
	return ui.app.Run()
}

// handleOperations processes UI operations in the main thread
func (ui *UI) handleOperations() {
	for op := range ui.opChan {
		ui.app.QueueUpdateDraw(op)
	}
}

// ShowError displays an error message
func (ui *UI) ShowError(msg string) {
	ui.opChan <- func() {
		timestamp := time.Now().Format("15:04:05")
		fmt.Fprintf(ui.debugView, "[gray]%s [red]Error:[white] %s\n", timestamp, msg)
		ui.debugView.ScrollToEnd()
	}
}

// LogDebug logs a debug message
func (ui *UI) LogDebug(msg string) {
	ui.opChan <- func() {
		timestamp := time.Now().Format("15:04:05")
		fmt.Fprintf(ui.debugView, "[gray]%s[white] %s\n", timestamp, msg)
		ui.debugView.ScrollToEnd()
	}
}

// ShowChat displays a chat message
func (ui *UI) ShowChat(from string, msg string) {
	ui.LogDebug(fmt.Sprintf("ShowChat called with from=%s, msg=%s", from, msg))
	
	if from == ui.token {
		ui.LogDebug("Showing chat message from self")
		ui.AppendChat(fmt.Sprintf("[yellow]You[white] %s", msg))
	} else {
		ui.LogDebug("Showing chat message from peer")
		ui.AppendChat(fmt.Sprintf("[yellow]Peer[white] %s", msg))
	}
}

// AppendChat appends a message to the chat view
func (ui *UI) AppendChat(msg string) {
	ui.LogDebug(fmt.Sprintf("AppendChat called with msg=%s", msg))
	
	ui.opChan <- func() {
		timestamp := time.Now().Format("15:04:05")
		fmt.Fprintf(ui.chatView, "[gray]%s[white] %s\n", timestamp, msg)
		ui.chatView.ScrollToEnd()
		
		ui.LogDebug("Message appended to chat view")
	}
}

// UpdateTransferProgress updates the transfer progress
func (ui *UI) UpdateTransferProgress(status string, direction string) {
	ui.opChan <- func() {
		// Add completed/failed transfers to history
		if strings.Contains(status, "Complete") || strings.Contains(status, "FAILED") {
			ui.transfers = append(ui.transfers, transfer{
				status:    status,
				timestamp: time.Now(),
			})
		}
		
		// Show transfer history, limited to last few entries
		maxHistory := 5
		start := 0
		if len(ui.transfers) > maxHistory {
			start = len(ui.transfers) - maxHistory
		}
		
		// Rebuild entire status display
		ui.fileView.Clear()
		
		// Show history first
		for i := start; i < len(ui.transfers); i++ {
			t := ui.transfers[i]
			fmt.Fprintf(ui.fileView, "[gray]%s[white] %s\n", 
				t.timestamp.Format("15:04:05"),
				t.status)
		}
		
		// Add space between history and active transfers
		if len(ui.transfers) > 0 && (strings.HasPrefix(status, "⬆") || strings.HasPrefix(status, "⬇")) {
			fmt.Fprintf(ui.fileView, "\n")
		}

		// Show active transfers
		if status != "" && !strings.Contains(status, "Complete") && !strings.Contains(status, "FAILED") {
			// Split transfers into send and receive sections
			if direction == "receive" {
				fmt.Fprintf(ui.fileView, "\n%s", status)
			} else {
				fmt.Fprintf(ui.fileView, "%s", status)
			}
		}
	}
}