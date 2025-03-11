package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ModelMsg struct {
Type string
Data interface{}
}

type UI struct {
program *tea.Program
client  *Client
msgChan chan tea.Msg
}

type model struct {
client         *Client
token          string
debugLog       string
inputPeerToken string
peerRequests   []string
error          string
}

func NewUI(client *Client) *UI {
ui := &UI{
client: client,
msgChan: make(chan tea.Msg),
}
initialModel := model{client: client}
p := tea.NewProgram(initialModel)
ui.program = p

// Setup message forwarder
go func() {
for msg := range ui.msgChan {
p.Send(msg)
}
}()

return ui
}

func (m model) Init() tea.Cmd {
return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
switch msg := msg.(type) {
case Message:
// Handle messages from the client
switch msg.Type {
case "token":
m.token = msg.Token
m.debugLog += "\nReceived token: " + msg.Token
case "request":
m.peerRequests = append(m.peerRequests, msg.Token)
m.debugLog += fmt.Sprintf("\nConnection request from: %s", msg.Token)
case "accepted":
m.debugLog += fmt.Sprintf("\nConnection accepted with: %s", msg.Token)
case "answer":
m.debugLog += fmt.Sprintf("\nReceived answer SDP")
case "ice":
m.debugLog += "\nReceived ICE candidate"
case "error":
m.error = msg.SDP
m.debugLog += "\nError: " + msg.SDP
}
case ModelMsg:
// Handle internal UI messages
switch msg.Type {
case "token":
m.token = msg.Data.(string)
case "request":
m.peerRequests = append(m.peerRequests, msg.Data.(string))
case "debug":
m.debugLog += "\n" + msg.Data.(string)
case "error":
m.error = msg.Data.(string)
m.debugLog += "\nError: " + msg.Data.(string)
}
case tea.KeyMsg:
switch msg.Type {
case tea.KeyEnter:
if m.inputPeerToken != "" {
token := m.inputPeerToken
m.inputPeerToken = ""
go m.client.Connect(token) // Run in goroutine
m.debugLog += fmt.Sprintf("\nAttempting to connect to: %s", token)
} else if len(m.peerRequests) > 0 {
// Accept the most recent request
token := m.peerRequests[len(m.peerRequests)-1]
m.peerRequests = m.peerRequests[:len(m.peerRequests)-1]
go m.client.Accept(token) // Run in goroutine
m.debugLog += fmt.Sprintf("\nAccepting connection from: %s", token)
}
case tea.KeyCtrlC:
return m, tea.Quit
case tea.KeyRunes:
m.inputPeerToken += string(msg.Runes)
case tea.KeyBackspace:
if len(m.inputPeerToken) > 0 {
m.inputPeerToken = m.inputPeerToken[:len(m.inputPeerToken)-1]
}
case tea.KeyEsc:
if len(m.peerRequests) > 0 {
// Reject the most recent request
token := m.peerRequests[len(m.peerRequests)-1]
m.peerRequests = m.peerRequests[:len(m.peerRequests)-1]
go m.client.Reject(token) // Run in goroutine
m.debugLog += fmt.Sprintf("\nRejected connection from: %s", token)
}
}
}
return m, nil
}

func (m model) View() string {
const width = 70

headerStyle := lipgloss.NewStyle().
Foreground(lipgloss.Color("6")).
BorderStyle(lipgloss.RoundedBorder()).
Padding(0, 1)

tokenField := headerStyle.Render(fmt.Sprintf("Your Token: %s", m.token))

helpText := lipgloss.NewStyle().
Foreground(lipgloss.Color("241")).
Render("Enter: Connect/Accept • Esc: Reject • Ctrl+C: Quit")

debugSection := lipgloss.NewStyle().
Border(lipgloss.RoundedBorder()).
MaxHeight(10).
Render(fmt.Sprintf("Debug Log:\n%s", m.debugLog))

var requestsContent string
if len(m.peerRequests) > 0 {
requestsContent = fmt.Sprintf("Pending Requests:\n%s", strings.Join(m.peerRequests, "\n"))
} else {
requestsContent = "No pending requests"
}
peerSection := lipgloss.NewStyle().
Border(lipgloss.RoundedBorder()).
Render(requestsContent)

inputField := lipgloss.NewStyle().
Border(lipgloss.RoundedBorder()).
Render(fmt.Sprintf("Enter Peer Token: %s", m.inputPeerToken))

if m.error != "" {
errorSection := lipgloss.NewStyle().
Foreground(lipgloss.Color("9")).
Border(lipgloss.RoundedBorder()).
Render(fmt.Sprintf("Error: %s", m.error))
inputField = fmt.Sprintf("%s\n\n%s", errorSection, inputField)
}

layout := lipgloss.NewStyle().
Width(width).
Align(lipgloss.Center)

return layout.Render(
fmt.Sprintf("%s\n\n%s\n\n%s\n\n%s\n\n%s",
tokenField,
helpText,
debugSection,
peerSection,
inputField,
),
)
}

// UI interface implementation methods that update the program state
func (ui *UI) SetToken(token string) {
ui.msgChan <- Message{Type: "token", Token: token}
}

func (ui *UI) ShowConnectionRequest(token string) {
ui.msgChan <- Message{Type: "request", Token: token}
}

func (ui *UI) ShowConnectionAccepted(msg string) {
ui.msgChan <- ModelMsg{Type: "debug", Data: msg}
}

func (ui *UI) ShowConnectionRejected(token string) {
ui.msgChan <- ModelMsg{Type: "debug", Data: fmt.Sprintf("Rejected connection with %s", token)}
}

func (ui *UI) ShowError(msg string) {
ui.msgChan <- Message{Type: "error", SDP: msg}
}

func (ui *UI) LogDebug(msg string) {
ui.msgChan <- ModelMsg{Type: "debug", Data: msg}
}

func (ui *UI) Run() error {
_, err := ui.program.Run()
close(ui.msgChan)
return err
}
