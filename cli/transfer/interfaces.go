package transfer

// Logger interface for logging and UI updates
type Logger interface {
    LogDebug(msg string)   // For debug messages (only shown with -debug flag)
    ShowError(msg string)  // For error messages (always shown)
    ShowInfo(msg string)   // For important info (always shown)
    AppendChat(msg string) // Show chat message in UI
    ShowChat(from, msg string) // Show chat message with sender info
}

// ProgressCallback is a function that updates the transfer progress
type ProgressCallback = func(status string, direction string)
