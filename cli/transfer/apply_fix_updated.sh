#!/bin/bash
# Script to fix the chat message handling in the CLI client

# Make backups of the original files
cp receive.go receive.go.bak
cp send.go send.go.bak

# Replace the original receive.go with the fixed version
cp receive_fixed_complete.go receive.go

# Remove the logger.go file if it exists
if [ -f logger.go ]; then
    rm logger.go
fi

echo "Fixed chat message handling in the CLI client."
echo "Original files backed up as receive.go.bak and send.go.bak."