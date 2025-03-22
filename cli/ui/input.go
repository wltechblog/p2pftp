package ui

// This is a deprecated file that will be removed
// Keeping minimal stubs to allow clean migration

type oldUI struct{}

func (u *oldUI) handleCommand(cmd string)                    {}
func (u *oldUI) ShowConnectionRequest(token string)          {}
func (u *oldUI) ShowConnectionAccepted(msg string)          {}
func (u *oldUI) ShowConnectionRejected(token string)        {}
func (u *oldUI) SetToken(token string)                      {}
