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
chmod +x apply_fix_updated.sh
./apply_fix_updated.sh
```

This will:
1. Make backups of the original files
2. Replace `receive.go` with the fixed version
3. Remove any conflicting files

## Manual Fix

If you prefer to apply the fix manually:

1. Edit `receive.go` and replace the "message" case in the `HandleControlMessage` method:

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

## After Applying the Fix

After applying the fix, rebuild the CLI client:

```bash
cd cli
go build -o p2pftp-cli
```

Now chat messages should be properly displayed in the CLI client when received from a peer.

## What Changed

The fix adds code to extract the content from the message and display it in the chat UI using the `AppendChat` method that's already available in the UI.

The "control channel buffer amount low" message you were seeing is just a debug message related to the WebRTC data channel's buffer, not an error. It's unrelated to the chat message issue.