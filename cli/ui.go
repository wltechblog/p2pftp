package main

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type UI struct {
app           *tview.Application
client        *Client
header        *tview.TextView
mainView      *tview.TextView
debugView     *tview.TextView
connectionBox *tview.Form
statusBar     *tview.TextView
pages         *tview.Pages
inputField    *tview.InputField
}

func NewUI(client *Client) *UI {
	ui := &UI{
		app:    tview.NewApplication(),
		client: client,
	}

	// Create header
	ui.header = tview.NewTextView().
		SetTextAlign(tview.AlignCenter).
		SetText("P2P File Transfer Client")

// Create main view
ui.mainView = tview.NewTextView().
SetChangedFunc(func() {
ui.app.Draw()
})
ui.mainView.SetBorder(true).SetTitle("Status")

// Create debug view
ui.debugView = tview.NewTextView().
SetChangedFunc(func() {
ui.app.Draw()
}).
SetTextColor(tcell.ColorYellow)
ui.debugView.SetBorder(true).SetTitle("Debug Log")

// Create connection form with debug logging for every event
ui.connectionBox = tview.NewForm()

// Initialize input field
ui.inputField = tview.NewInputField()
ui.inputField.SetLabel("Peer Token: ")
ui.inputField.SetFieldWidth(20)

ui.inputField.SetChangedFunc(func(text string) {
	ui.LogDebug(fmt.Sprintf("Text changed: '%s'", text))
	if text == ui.client.token {
		ui.ShowError("Cannot connect to yourself")
	}
})

ui.inputField.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
	ui.LogDebug(fmt.Sprintf("Key pressed: %v", event.Key()))
	if event.Key() == tcell.KeyEnter {
		token := ui.inputField.GetText()
		ui.LogDebug(fmt.Sprintf("Enter pressed with token: '%s'", token))

		if token == "" {
			ui.ShowError("Please enter a peer token")
			return nil
		}

		if token == ui.client.token {
			ui.ShowError("Cannot connect to yourself")
			return nil
		}

		ui.LogDebug("Initiating connection...")
		if err := ui.client.Connect(token); err != nil {
			ui.ShowError(fmt.Sprintf("Failed to connect: %v", err))
			ui.LogDebug(fmt.Sprintf("Connect error: %v", err))
		} else {
			ui.Printf("Sending connection request to peer: %s...\n", token)
		}
		ui.inputField.SetText("")
	}
	return event
})

// Add inputField to the form
ui.connectionBox.AddFormItem(ui.inputField)
ui.connectionBox.SetBorder(true).SetTitle("Connect to Peer")

	// Create status bar
	ui.statusBar = tview.NewTextView().
		SetTextColor(tview.Styles.TertiaryTextColor)

	// Create layout
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(ui.header, 3, 1, false).
AddItem(tview.NewFlex().SetDirection(tview.FlexColumn).
AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
AddItem(ui.mainView, 0, 2, false).
AddItem(ui.debugView, 0, 1, false),
0, 2, false).
AddItem(ui.connectionBox, 30, 1, true),
0, 1, true).
		AddItem(ui.statusBar, 1, 1, false)

	// Create pages for modal dialogs
	ui.pages = tview.NewPages().
		AddPage("main", flex, true, true)

	return ui
}

func (ui *UI) Run() error {
	return ui.app.SetRoot(ui.pages, true).Run()
}

func (ui *UI) SetToken(token string) {
	ui.app.QueueUpdateDraw(func() {
		ui.header.SetText(fmt.Sprintf("Your Token: %s", token))
	})
}

func (ui *UI) ShowError(msg string) {
	ui.app.QueueUpdateDraw(func() {
		ui.statusBar.SetText(fmt.Sprintf("Error: %s", msg))
	})
}

func (ui *UI) Printf(format string, args ...interface{}) {
	ui.app.QueueUpdateDraw(func() {
		fmt.Fprintf(ui.mainView, format, args...)
	})
}

func (ui *UI) ShowConnectionRequest(peerToken string) {
	modal := tview.NewModal().
		SetText(fmt.Sprintf("Connection request from peer: %s\nAccept connection?", peerToken)).
		AddButtons([]string{"Accept", "Reject"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "Accept" {
				ui.client.Accept(peerToken)
				ui.Printf("Accepted connection from: %s\n", peerToken)
			} else {
				ui.client.Reject(peerToken)
				ui.Printf("Rejected connection from: %s\n", peerToken)
			}
			ui.pages.SwitchToPage("main")
		})

	ui.app.QueueUpdateDraw(func() {
		ui.pages.AddPage("modal", modal, true, true)
	})
}

func (ui *UI) ShowConnectionAccepted(peerToken string) {
	ui.Printf("Peer %s accepted the connection\n", peerToken)
}

func (ui *UI) ShowConnectionRejected(peerToken string) {
ui.Printf("Peer %s rejected the connection\n", peerToken)
}

func (ui *UI) LogDebug(msg string) {
ui.app.QueueUpdateDraw(func() {
ts := time.Now().Format("15:04:05")
fmt.Fprintf(ui.debugView, "[%s] %s\n", ts, msg)
})
}
