# Chat Message Fix

This directory contains a fix for the chat message handling in the CLI client. The issue is that chat messages sent from one CLI client to another are not being displayed in the receiving client's UI.

## The Issue

The problem is in the `HandleControlMessage` method in `receive.go`. When it receives a chat message (type "message"), it simply returns nil without doing anything with the message:

```go
case "message":
    // Chat messages are handled by the channels component
    return nil
```

However, the Receiver is being used as the message handler in the Channels setup, so it needs to properly handle chat messages.

## How to Apply the Fix

The simplest way to apply the fix is to use the provided script:

```bash
cd cli/transfer
chmod +x apply_fix.sh
./apply_fix.sh
```

This will:
1. Make a backup of the original `receive.go` file
2. Replace it with the fixed version

## After Applying the Fix

After applying the fix, rebuild the CLI client:

```bash
cd cli
go build -o p2pftp-cli
```

Now chat messages should be properly displayed in the CLI client when received from a peer.

## What Changed

The fix adds code to extract the content from the message and display it in the chat UI using the `AppendChat` method:

```go
case "message":
    // Handle chat message
    content, ok := message["content"].(string)
    if !ok {
        return fmt.Errorf("invalid message format: missing content")
    }
    
    // Display the chat message
    r.logger.AppendChat(fmt.Sprintf("[yellow]Peer[white] %s", content))
    return nil
```

It also updates the `Logger` interface in `send.go` to include the `AppendChat` method:

```go
// Logger interface for logging
type Logger interface {
    LogDebug(msg string)
    ShowError(msg string)
    AppendChat(msg string)
}
```