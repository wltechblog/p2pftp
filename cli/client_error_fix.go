package main

// This file contains the improved error handling for the client

// handleErrorMessage handles an error message from the server
func (c *Client) handleErrorMessage(msg Message) {
	errorMsg := "Connection error"
	if msg.SDP != "" {
		errorMsg = msg.SDP
	}
	c.logMessage("Received error message: %s", errorMsg)
	c.ui.ShowError(errorMsg)
	c.Disconnect()
}