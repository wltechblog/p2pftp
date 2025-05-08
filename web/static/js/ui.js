/**
 * P2PFTP UI Implementation
 * This file handles the user interface
 */

document.addEventListener('DOMContentLoaded', () => {
    // UI Elements
    const elements = {
        // Connection panel
        connectionPanel: document.getElementById('connection-panel'),
        tokenDisplay: document.getElementById('token-display'),
        myToken: document.getElementById('my-token'),
        connectionLink: document.getElementById('connection-link'),
        copyLink: document.getElementById('copy-link'),
        connectForm: document.getElementById('connect-form'),
        serverUrl: document.getElementById('server-url'),
        peerToken: document.getElementById('peer-token'),
        connectButton: document.getElementById('connect-button'),
        connectPeerButton: document.getElementById('connect-peer-button'),
        connectionStatus: document.getElementById('connection-status'),
        
        // File transfer panel
        fileTransferPanel: document.getElementById('file-transfer-panel'),
        fileInput: document.getElementById('file-input'),
        fileSelectButton: document.getElementById('file-select-button'),
        selectedFileName: document.getElementById('selected-file-name'),
        sendFileButton: document.getElementById('send-file-button'),
        transferProgress: document.getElementById('transfer-progress'),
        transferFilename: document.getElementById('transfer-filename'),
        transferStatus: document.getElementById('transfer-status'),
        progressBar: document.getElementById('progress-bar'),
        bytesTransferred: document.getElementById('bytes-transferred'),
        transferSpeed: document.getElementById('transfer-speed'),
        timeRemaining: document.getElementById('time-remaining'),
        cancelTransferButton: document.getElementById('cancel-transfer-button'),
        
        // Chat panel
        chatPanel: document.getElementById('chat-panel'),
        chatMessages: document.getElementById('chat-messages'),
        chatInput: document.getElementById('chat-input'),
        sendMessageButton: document.getElementById('send-message-button'),
        
        // Status log panel
        statusLogPanel: document.getElementById('status-log-panel'),
        debugMode: document.getElementById('debug-mode'),
        logContainer: document.getElementById('log-container'),
        
        // Connection request modal
        connectionRequestModal: document.getElementById('connection-request-modal'),
        requestPeerToken: document.getElementById('request-peer-token'),
        rejectConnection: document.getElementById('reject-connection'),
        acceptConnection: document.getElementById('accept-connection')
    };
    
    // Logger
    const logger = {
        log: function(message, ...args) {
            console.log(message, ...args);
            addLogEntry('info', message + (args.length > 0 ? ' ' + args.join(' ') : ''));
        },
        error: function(message, ...args) {
            console.error(message, ...args);
            addLogEntry('error', message + (args.length > 0 ? ' ' + args.join(' ') : ''));
        },
        warn: function(message, ...args) {
            console.warn(message, ...args);
            addLogEntry('warning', message + (args.length > 0 ? ' ' + args.join(' ') : ''));
        },
        debug: function(message, ...args) {
            console.debug(message, ...args);
            if (elements.debugMode.checked) {
                addLogEntry('debug', message + (args.length > 0 ? ' ' + args.join(' ') : ''));
            }
        }
    };
    
    // Add log entry to the log container
    function addLogEntry(level, message) {
        const entry = document.createElement('div');
        entry.className = `log-entry ${level}`;
        entry.textContent = message;
        elements.logContainer.appendChild(entry);
        elements.logContainer.scrollTop = elements.logContainer.scrollHeight;
    }
    
    // Format bytes to human-readable format
    function formatBytes(bytes, decimals = 2) {
        if (bytes === 0) return '0 B';
        
        const k = 1024;
        const dm = decimals < 0 ? 0 : decimals;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'];
        
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        
        return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
    }
    
    // Format seconds to MM:SS format
    function formatTime(seconds) {
        if (!isFinite(seconds) || seconds < 0) {
            return '--:--';
        }
        
        const minutes = Math.floor(seconds / 60);
        const secs = Math.floor(seconds % 60);
        
        return `${minutes.toString().padStart(2, '0')}:${secs.toString().padStart(2, '0')}`;
    }
    
    // Initialize P2P connection
    const p2p = new P2PConnection(logger);
    
    // Initialize file transfer
    const fileTransfer = new FileTransfer(p2p, logger);
    
    // Set up event handlers
    p2p.onStatusChange = (status) => {
        elements.connectionStatus.textContent = status;
        
        // Check if connected to peer
        if (p2p.isConnected()) {
            // Show file transfer and chat panels
            elements.fileTransferPanel.classList.remove('hidden');
            elements.chatPanel.classList.remove('hidden');
            
            // Enable send file button if file is selected
            if (elements.fileInput.files.length > 0) {
                elements.sendFileButton.disabled = false;
            }
        } else {
            // Disable connect peer button if not connected to server
            if (!p2p.token) {
                elements.connectPeerButton.disabled = true;
            }
        }
    };
    
    p2p.onTokenReceived = (token) => {
        // Display token and connection link
        elements.myToken.textContent = token;
        elements.connectionLink.value = `https://${elements.serverUrl.value}/?token=${token}`;
        elements.tokenDisplay.classList.remove('hidden');
        
        // Enable connect peer button
        elements.connectPeerButton.disabled = false;
    };
    
    p2p.onConnectionRequest = (peerToken) => {
        // Show connection request modal
        elements.requestPeerToken.textContent = peerToken;
        elements.connectionRequestModal.classList.remove('hidden');
        
        // Store peer token for accept/reject
        elements.connectionRequestModal.dataset.peerToken = peerToken;
    };
    
    p2p.onMessage = (message) => {
        // Add message to chat
        addChatMessage(message, false);
    };
    
    p2p.onError = (error) => {
        logger.error('P2P error:', error);
    };
    
    // File transfer event handlers
    fileTransfer.onProgress = (progress) => {
        // Update progress bar
        elements.progressBar.style.width = `${progress.percent}%`;
        
        // Update transfer status
        if (fileTransfer.sending) {
            elements.transferStatus.textContent = 'Sending...';
            elements.bytesTransferred.textContent = `${formatBytes(progress.bytesSent)} / ${formatBytes(progress.totalBytes)}`;
        } else {
            elements.transferStatus.textContent = 'Receiving...';
            elements.bytesTransferred.textContent = `${formatBytes(progress.bytesReceived)} / ${formatBytes(progress.totalBytes)}`;
        }
        
        // Update transfer speed
        elements.transferSpeed.textContent = `${formatBytes(progress.speed)}/s`;
        
        // Update time remaining
        elements.timeRemaining.textContent = formatTime(progress.timeRemaining);
    };
    
    fileTransfer.onComplete = () => {
        // Update transfer status
        elements.transferStatus.textContent = 'Complete';
        elements.progressBar.style.width = '100%';
        
        // Hide cancel button
        elements.cancelTransferButton.classList.add('hidden');
        
        // Reset file input after a delay
        setTimeout(() => {
            elements.fileInput.value = '';
            elements.selectedFileName.textContent = 'No file selected';
            elements.sendFileButton.disabled = true;
            elements.transferProgress.classList.add('hidden');
        }, 3000);
    };
    
    fileTransfer.onError = (error) => {
        // Update transfer status
        elements.transferStatus.textContent = `Error: ${error.message}`;
        
        // Hide cancel button
        elements.cancelTransferButton.classList.add('hidden');
        
        // Reset file input after a delay
        setTimeout(() => {
            elements.fileInput.value = '';
            elements.selectedFileName.textContent = 'No file selected';
            elements.sendFileButton.disabled = true;
            elements.transferProgress.classList.add('hidden');
        }, 3000);
    };
    
    fileTransfer.onFileInfo = (info) => {
        // Show transfer progress
        elements.transferProgress.classList.remove('hidden');
        elements.transferFilename.textContent = info.name;
        elements.transferStatus.textContent = 'Receiving...';
        elements.progressBar.style.width = '0%';
        elements.bytesTransferred.textContent = `0 B / ${formatBytes(info.size)}`;
        elements.transferSpeed.textContent = '0 B/s';
        elements.timeRemaining.textContent = '--:--';
        elements.cancelTransferButton.classList.remove('hidden');
    };
    
    fileTransfer.onVerificationStart = () => {
        elements.transferStatus.textContent = 'Verifying...';
    };
    
    fileTransfer.onVerificationComplete = (blob, fileInfo) => {
        elements.transferStatus.textContent = 'Verified';
        
        // Create download link
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = fileInfo.name;
        a.style.display = 'none';
        document.body.appendChild(a);
        a.click();
        
        // Clean up
        setTimeout(() => {
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
        }, 100);
    };
    
    fileTransfer.onVerificationFailed = (reason) => {
        elements.transferStatus.textContent = `Verification failed: ${reason}`;
    };
    
    // Connect to server button
    elements.connectButton.addEventListener('click', async () => {
        try {
            // Disable button during connection
            elements.connectButton.disabled = true;
            elements.connectButton.textContent = 'Connecting...';
            
            // Initialize peer connection
            p2p.initializePeerConnection();
            
            // Connect to server
            await p2p.connectToServer(elements.serverUrl.value);
            
            // Check if peer token is provided
            if (elements.peerToken.value) {
                // Connect to peer
                p2p.connectToPeer(elements.peerToken.value);
            }
            
            // Re-enable button
            elements.connectButton.disabled = false;
            elements.connectButton.textContent = 'Connect to Server';
        } catch (error) {
            logger.error('Connection error:', error);
            
            // Re-enable button
            elements.connectButton.disabled = false;
            elements.connectButton.textContent = 'Connect to Server';
        }
    });
    
    // Connect to peer button
    elements.connectPeerButton.addEventListener('click', () => {
        // Check if peer token is provided
        if (elements.peerToken.value) {
            // Connect to peer
            p2p.connectToPeer(elements.peerToken.value);
        } else {
            logger.error('Peer token is required');
        }
    });
    
    // Copy link button
    elements.copyLink.addEventListener('click', () => {
        // Copy connection link to clipboard
        elements.connectionLink.select();
        document.execCommand('copy');
        
        // Show copied message
        const originalText = elements.copyLink.textContent;
        elements.copyLink.textContent = 'Copied!';
        
        // Reset button text after a delay
        setTimeout(() => {
            elements.copyLink.textContent = originalText;
        }, 2000);
    });
    
    // File select button
    elements.fileSelectButton.addEventListener('click', () => {
        elements.fileInput.click();
    });
    
    // File input change
    elements.fileInput.addEventListener('change', () => {
        if (elements.fileInput.files.length > 0) {
            const file = elements.fileInput.files[0];
            elements.selectedFileName.textContent = `${file.name} (${formatBytes(file.size)})`;
            
            // Enable send button if connected
            if (p2p.isConnected()) {
                elements.sendFileButton.disabled = false;
            }
        } else {
            elements.selectedFileName.textContent = 'No file selected';
            elements.sendFileButton.disabled = true;
        }
    });
    
    // Send file button
    elements.sendFileButton.addEventListener('click', async () => {
        if (elements.fileInput.files.length > 0) {
            try {
                const file = elements.fileInput.files[0];
                
                // Show transfer progress
                elements.transferProgress.classList.remove('hidden');
                elements.transferFilename.textContent = file.name;
                elements.transferStatus.textContent = 'Sending...';
                elements.progressBar.style.width = '0%';
                elements.bytesTransferred.textContent = `0 B / ${formatBytes(file.size)}`;
                elements.transferSpeed.textContent = '0 B/s';
                elements.timeRemaining.textContent = '--:--';
                elements.cancelTransferButton.classList.remove('hidden');
                
                // Disable send button
                elements.sendFileButton.disabled = true;
                
                // Send file
                await fileTransfer.sendFile(file);
            } catch (error) {
                logger.error('Error sending file:', error);
                
                // Hide transfer progress
                elements.transferProgress.classList.add('hidden');
                
                // Re-enable send button
                elements.sendFileButton.disabled = false;
            }
        }
    });
    
    // Cancel transfer button
    elements.cancelTransferButton.addEventListener('click', () => {
        fileTransfer.cancelTransfer();
    });
    
    // Send message button
    elements.sendMessageButton.addEventListener('click', () => {
        sendChatMessage();
    });
    
    // Chat input enter key
    elements.chatInput.addEventListener('keydown', (event) => {
        if (event.key === 'Enter') {
            sendChatMessage();
        }
    });
    
    // Send chat message
    function sendChatMessage() {
        const message = elements.chatInput.value.trim();
        
        if (message && p2p.isConnected()) {
            // Send message
            p2p.sendChatMessage(message);
            
            // Add message to chat
            addChatMessage(message, true);
            
            // Clear input
            elements.chatInput.value = '';
        }
    }
    
    // Add chat message
    function addChatMessage(message, sent) {
        const messageElement = document.createElement('div');
        messageElement.className = `message ${sent ? 'sent' : 'received'}`;
        messageElement.textContent = message;
        
        elements.chatMessages.appendChild(messageElement);
        elements.chatMessages.scrollTop = elements.chatMessages.scrollHeight;
    }
    
    // Accept connection button
    elements.acceptConnection.addEventListener('click', () => {
        const peerToken = elements.connectionRequestModal.dataset.peerToken;
        
        if (peerToken) {
            // Accept connection
            p2p.acceptConnection(peerToken);
            
            // Hide modal
            elements.connectionRequestModal.classList.add('hidden');
        }
    });
    
    // Reject connection button
    elements.rejectConnection.addEventListener('click', () => {
        const peerToken = elements.connectionRequestModal.dataset.peerToken;
        
        if (peerToken) {
            // Reject connection
            p2p.rejectConnection(peerToken);
            
            // Hide modal
            elements.connectionRequestModal.classList.add('hidden');
        }
    });
    
    // Debug mode toggle
    elements.debugMode.addEventListener('change', () => {
        if (elements.debugMode.checked) {
            logger.log('Debug mode enabled');
        } else {
            logger.log('Debug mode disabled');
        }
    });
    
    // Check for connection token in URL
    function checkUrlForToken() {
        const urlParams = new URLSearchParams(window.location.search);
        const token = urlParams.get('token');
        
        if (token) {
            // Set peer token
            elements.peerToken.value = token;
            
            // Auto-connect if token is present
            logger.log('Found token in URL, auto-connecting...');
            elements.connectButton.click();
        }
    }
    
    // Initialize
    checkUrlForToken();
});