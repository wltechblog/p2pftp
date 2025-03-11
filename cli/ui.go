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
}
initialModel := model{client: client}
ui.program = tea.NewProgram(initialModel)
return ui
}

func (m model) Init() tea.Cmd {
return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
switch msg := msg.(type) {
case ModelMsg:
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
m.client.Connect(token)
} else if len(m.peerRequests) > 0 {
// Accept the most recent request
token := m.peerRequests[len(m.peerRequests)-1]
m.peerRequests = m.peerRequests[:len(m.peerRequests)-1]
m.client.Accept(token)
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
m.client.Reject(token)
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
ui.program.Send(ModelMsg{Type: "token", Data: token})
}

func (ui *UI) ShowConnectionRequest(token string) {
ui.program.Send(ModelMsg{Type: "request", Data: token})
}

func (ui *UI) ShowConnectionAccepted(msg string) {
ui.program.Send(ModelMsg{Type: "debug", Data: msg})
}

func (ui *UI) ShowConnectionRejected(token string) {
ui.program.Send(ModelMsg{Type: "debug", Data: fmt.Sprintf("Rejected connection with %s", token)})
}

func (ui *UI) ShowError(msg string) {
ui.program.Send(ModelMsg{Type: "error", Data: msg})
}

func (ui *UI) LogDebug(msg string) {
ui.program.Send(ModelMsg{Type: "debug", Data: msg})
}

func (ui *UI) Run() error {
_, err := ui.program.Run()
return err
}
