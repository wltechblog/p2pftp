/**
 * P2PFTP UI Implementation
 * This file handles the user interface
 */

// Toggle panel collapse/expand state
function togglePanel(panelId) {
    const panel = document.getElementById(panelId);
    const content = document.getElementById(`${panelId}-content`);
    const icon = document.getElementById(`${panelId}-toggle-icon`);
    
    // Check if panel is currently collapsed (either has 'collapsed' class or is hidden)
    const isCollapsed = content.classList.contains('collapsed') || content.classList.contains('hidden');
    
    if (isCollapsed) {
        // Expand panel
        content.classList.remove('collapsed', 'hidden');
        content.classList.add('expanded');
        icon.classList.remove('rotate-180');
        panel.classList.remove('collapsed');
    } else {
        // Collapse panel
        content.classList.remove('expanded');
        content.classList.add('collapsed');
        icon.classList.add('rotate-180');
        panel.classList.add('collapsed');
    }
}

// Auto-detect server URL based on current page
function detectServerUrl() {
    try {
        const hostname = window.location.hostname;
        
        // Validate hostname
        if (!hostname) {
            console.warn('Could not detect hostname, using default');
            return 'p2p.teamworkapps.com';
        }
        
        // Check for localhost or IP addresses
        if (hostname === 'localhost' || hostname === '127.0.0.1') {
            console.log('Detected localhost, using default server');
            return 'p2p.teamworkapps.com';
        }
        
        // Check for invalid hostnames
        if (!isValidHostname(hostname)) {
            console.warn(`Invalid hostname detected: ${hostname}, using default`);
            return 'p2p.teamworkapps.com';
        }
        
        // Return only the hostname without port for the server URL input field
        // The WebSocket URL construction will handle the port automatically in the connection logic
        return hostname;
    } catch (error) {
        console.error('Error detecting server URL:', error);
        return 'p2p.teamworkapps.com';
    }
}

/**
 * Validate hostname format
 * @param {string} hostname - The hostname to validate
 * @returns {boolean} - True if valid, false otherwise
 */
function isValidHostname(hostname) {
    if (!hostname || typeof hostname !== 'string') {
        return false;
    }
    
    // Basic hostname validation
    const hostnameRegex = /^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$/;
    
    // Check length (max 253 characters)
    if (hostname.length > 253) {
        return false;
    }
    
    // Check against regex
    return hostnameRegex.test(hostname);
}

// Initialize UI when DOM is ready
function initUI() {
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
        capabilitiesStatus: document.getElementById('capabilities-status'),
        capabilitiesText: document.getElementById('capabilities-text'),
        
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
        
        // Transfer completion elements
        transferCompletion: document.getElementById('transfer-completion'),
        completionFilename: document.getElementById('completion-filename'),
        completionFilesize: document.getElementById('completion-filesize'),
        completionSpeed: document.getElementById('completion-speed'),
        completionVerification: document.getElementById('completion-verification'),
        downloadSection: document.getElementById('download-section'),
        downloadButton: document.getElementById('download-button'),
        
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
    
    // Transfer history storage
    let transferHistory = [];
    
    // Multiple transfer progress tracking
    let activeProgressIndicators = new Map(); // transferId -> DOM element
    
    // Add log entry to the log container
    function addLogEntry(level, message) {
        // Check current number of log entries and trim if exceeding 100
        const currentEntries = elements.logContainer.children;
        if (currentEntries.length >= 100) {
            // Remove oldest entries to maintain maximum of 100 lines
            const entriesToRemove = currentEntries.length - 99; // Keep 99 to make room for new entry
            for (let i = 0; i < entriesToRemove; i++) {
                elements.logContainer.removeChild(currentEntries[0]);
            }
        }
        
        const entry = document.createElement('div');
        entry.className = `log-entry ${level}`;
        entry.textContent = message;
        elements.logContainer.appendChild(entry);
        
        // Smooth scrolling to bottom after adding new entry
        elements.logContainer.scrollTop = elements.logContainer.scrollHeight;
    }
    
    // Add transfer to history
    function addToHistory(transferInfo) {
        const timestamp = new Date().toLocaleString();
        const historyItem = {
            ...transferInfo,
            timestamp: timestamp,
            id: Date.now()
        };
        
        transferHistory.unshift(historyItem);
        
        // Keep only last 10 transfers
        if (transferHistory.length > 10) {
            transferHistory = transferHistory.slice(0, 10);
        }
        
        updateHistoryDisplay();
    }
    
    // Update history display
    function updateHistoryDisplay() {
        const historyList = document.getElementById('transfer-history-list');
        
        if (transferHistory.length === 0) {
            historyList.innerHTML = '<div class="text-sm text-gray-500 italic">No transfers completed yet</div>';
            return;
        }
        
        historyList.innerHTML = '';
        
        transferHistory.forEach(item => {
            const historyEntry = document.createElement('div');
            historyEntry.className = 'p-3 bg-gray-50 rounded-md border border-gray-200';
            
            const isReceived = item.type === 'received';
            const statusIcon = isReceived ? '↓' : '↑';
            const statusColor = isReceived ? 'text-green-600' : 'text-blue-600';
            
            historyEntry.innerHTML = `
                <div class="flex justify-between items-start">
                    <div class="flex-1">
                        <div class="flex items-center mb-1">
                            <span class="${statusColor} font-medium mr-2">${statusIcon}</span>
                            <span class="font-medium text-gray-900">${item.filename}</span>
                        </div>
                        <div class="text-sm text-gray-600">
                            <span class="mr-3">${item.filesize}</span>
                            <span class="mr-3">${item.speed}</span>
                            <span class="text-xs text-gray-500">${item.timestamp}</span>
                        </div>
                    </div>
                    ${item.downloadUrl ? `
                        <button onclick="downloadFromHistory('${item.id}')" class="ml-2 bg-blue-500 text-white px-3 py-1 rounded text-sm hover:bg-blue-600 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-opacity-50">
                            Download
                        </button>
                    ` : ''}
                </div>
            `;
            
            historyList.appendChild(historyEntry);
        });
    }
    
    // Download file from history
    window.downloadFromHistory = function(transferId) {
        const transfer = transferHistory.find(item => item.id == transferId);
        if (transfer && transfer.downloadUrl) {
            try {
                const a = document.createElement('a');
                a.href = transfer.downloadUrl;
                a.download = transfer.filename;
                a.style.display = 'none';
                document.body.appendChild(a);
                a.click();
                
                setTimeout(() => {
                    document.body.removeChild(a);
                }, 100);
                
                logger.log('Download from history initiated successfully');
            } catch (error) {
                logger.error('Error downloading from history:', error);
            }
        }
    };
    
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
        // Update dual status display
        _updateConnectionStatus(status);
        
        // Handle capabilities exchange status
        if (status.includes('Negotiated chunk size')) {
            // Capabilities exchange complete
            elements.capabilitiesStatus.classList.add('hidden');
            logger.log('Capabilities exchange completed successfully');
        } else if (status.includes('Control channel opened') && !p2p.capabilitiesExchanged) {
            // Show capabilities exchange status
            elements.capabilitiesStatus.classList.remove('hidden');
            elements.capabilitiesText.textContent = 'Exchanging capabilities...';
        }
        
        // Check if connected to peer
        if (p2p.isConnected()) {
            // Show file transfer and chat panels
            elements.fileTransferPanel.classList.remove('hidden');
            elements.chatPanel.classList.remove('hidden');
            
            // Auto-collapse connection panel when peer connects
            const connectionContent = document.getElementById('connection-panel-content');
            const connectionIcon = document.getElementById('connection-panel-toggle-icon');
            const connectionPanel = document.getElementById('connection-panel');
            
            if (!connectionContent.classList.contains('collapsed')) {
                connectionContent.classList.remove('expanded');
                connectionContent.classList.add('collapsed');
                connectionIcon.classList.add('rotate-180');
                connectionPanel.classList.add('collapsed');
            }
            
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
        // Check if this is a multi-transfer progress update
        if (progress.transferId) {
            // Handle multiple transfer progress
            updateMultipleTransferProgress(progress);
        } else {
            // Handle legacy single transfer progress
            updateSingleTransferProgress(progress);
        }
    };
    
    // Update single transfer progress (legacy compatibility)
    function updateSingleTransferProgress(progress) {
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
    }
    
    // Update multiple transfer progress
    function updateMultipleTransferProgress(progress) {
        let progressElement = activeProgressIndicators.get(progress.transferId);
        
        if (!progressElement) {
            // Create new progress indicator for this transfer
            progressElement = createProgressIndicator(progress);
            activeProgressIndicators.set(progress.transferId, progressElement);
        }
        
        // Update the progress indicator
        
        // CRITICAL FIX: Update receiving side status for multiple transfers
        // Ensure main transfer status is updated for receiving transfers
        if (progress.complete || progress.percent === 100) {
            const isReceiving = progress.bytesReceived !== undefined;
            if (isReceiving && fileTransfer.receiving) {
                elements.transferStatus.textContent = 'Complete';
                elements.progressBar.style.width = '100%';
                elements.progressBar.className = 'bg-green-500 h-3 rounded-full transition-all duration-300';
                
                // Update bytes transferred to show full completion
                if (elements.bytesTransferred && progress.totalBytes) {
                    elements.bytesTransferred.textContent = `${formatBytes(progress.totalBytes)} / ${formatBytes(progress.totalBytes)}`;
                }
                
                // Update time remaining to show completion
                if (elements.timeRemaining) {
                    elements.timeRemaining.textContent = '00:00';
                }
            }
        }
        updateProgressIndicator(progressElement, progress);
    }
    
    // Create a new progress indicator for a transfer
    function createProgressIndicator(progress) {
        const container = document.getElementById('transfer-progress-container');
        
        // Hide the single transfer progress if visible
        const singleProgress = document.getElementById('transfer-progress');
        if (singleProgress) {
            singleProgress.classList.add('hidden');
        }
        
        const indicator = document.createElement('div');
        indicator.className = 'border rounded-lg p-4 bg-white shadow-sm';
        indicator.id = progress.transferId;
        
        const isSending = progress.bytesSent !== undefined;
        const bytesTransferred = isSending ? progress.bytesSent : progress.bytesReceived;
        
        indicator.innerHTML = `
            <div class="mb-2 flex justify-between items-center">
                <span class="font-medium text-gray-900">${progress.filename || 'Unknown File'}</span>
                <span class="text-sm text-gray-600">${isSending ? 'Sending...' : 'Receiving...'}</span>
                <button onclick="cancelTransfer('${progress.transferId}')" class="ml-2 bg-red-500 text-white px-3 py-1 rounded text-sm hover:bg-red-600 focus:outline-none focus:ring-2 focus:ring-red-500 focus:ring-opacity-50">
                    Cancel
                </button>
            </div>
            <div class="w-full bg-gray-200 rounded-full h-3 mb-2">
                <div class="bg-blue-500 h-3 rounded-full transition-all duration-300" style="width: ${progress.percent}%"></div>
            </div>
            <div class="flex justify-between text-sm text-gray-600">
                <span>${formatBytes(bytesTransferred)} / ${formatBytes(progress.totalBytes)}</span>
                <span>${formatBytes(progress.speed)}/s</span>
                <span>${formatTime(progress.timeRemaining)}</span>
            </div>
        `;
        
        container.appendChild(indicator);
        return indicator;
    }
    
    // Update an existing progress indicator
    function updateProgressIndicator(indicator, progress) {
        const progressBar = indicator.querySelector('.bg-blue-500');
        
        // Find the status, bytes, speed, and time elements more reliably
        const flexContainer = indicator.querySelector('.justify-between.text-sm.text-gray-600');
        const statusElement = indicator.querySelector('.text-gray-600'); // This is the status span in the header
        
        // Get the three spans from the flex container
        const spans = flexContainer ? flexContainer.querySelectorAll('span') : [];
        const bytesElement = spans[0]; // First span: bytes transferred
        const speedElement = spans[1]; // Second span: speed
        const timeElement = spans[2]; // Third span: time remaining
        
        const isSending = progress.bytesSent !== undefined;
        const bytesTransferred = isSending ? progress.bytesSent : progress.bytesReceived;
        
        // Update progress bar
        if (progressBar) {
            progressBar.style.width = `${progress.percent}%`;
        }
        
        // Update status
        if (statusElement) {
            statusElement.textContent = isSending ? 'Sending...' : 'Receiving...';
        }
        
        // Update stats with null checks
        if (bytesElement) {
            bytesElement.textContent = `${formatBytes(bytesTransferred)} / ${formatBytes(progress.totalBytes)}`;
        }
        if (speedElement) {
            speedElement.textContent = `${formatBytes(progress.speed)}/s`;
        }
        if (timeElement) {
            timeElement.textContent = formatTime(progress.timeRemaining);
        }
        
        // CRITICAL FIX: Update progress indicator status when complete
        if (progress.complete || progress.percent === 100) {
            if (progressBar) {
                progressBar.style.width = '100%';
                progressBar.className = 'bg-green-500 h-3 rounded-full transition-all duration-300';
            }
            
            if (statusElement) {
                statusElement.textContent = 'Complete';
            }
            
            if (timeElement) {
                timeElement.textContent = '00:00';
            }
        }
    }
    
    // Remove a progress indicator
    function removeProgressIndicator(transferId) {
        const indicator = activeProgressIndicators.get(transferId);
        if (indicator) {
            indicator.remove();
            activeProgressIndicators.delete(transferId);
        }
        
        // Show single transfer progress if no multiple transfers are active
        if (activeProgressIndicators.size === 0) {
            const singleProgress = document.getElementById('transfer-progress');
            if (singleProgress) {
                singleProgress.classList.remove('hidden');
            }
        }
    }
    
    // Cancel a specific transfer
    window.cancelTransfer = function(transferId) {
        const transfer = fileTransfer.activeTransfers.get(transferId);
        if (transfer) {
            transfer.transferCancelled = true;
            logger.log(`Cancelled transfer ${transferId}: ${transfer.file.name}`);
        }
    };
    
    fileTransfer.onComplete = () => {
        // Handle multiple transfer completion
        const completedTransfers = Array.from(fileTransfer.activeTransfers.values())
            .filter(t => t.transferComplete && !t.completedHandled);
        
        for (const transfer of completedTransfers) {
            transfer.completedHandled = true;
            
            // Update progress indicator to show completion
            const progressElement = activeProgressIndicators.get(transfer.id);
            if (progressElement) {
                const progressBar = progressElement.querySelector('.bg-blue-500');
                const statusElement = progressElement.querySelector('.text-gray-600');
                
                progressBar.style.width = '100%';
                progressBar.className = 'bg-green-500 h-3 rounded-full transition-all duration-300';
                statusElement.textContent = 'Complete';
                
                // Remove cancel button
                const cancelButton = progressElement.querySelector('button');
                if (cancelButton) {
                    cancelButton.remove();
                }
                
                // Remove progress indicator after delay
                setTimeout(() => {
                    removeProgressIndicator(transfer.id);
                }, 3000);
            }
        }
        
        // Check if all transfers are complete
        const allTransfersComplete = Array.from(fileTransfer.activeTransfers.values())
            .every(t => t.transferComplete);
        
        if (allTransfersComplete) {
            // Calculate final transfer speed for legacy compatibility
            const transferTime = (Date.now() - fileTransfer.startTime) / 1000; // seconds
            const finalSpeed = fileTransfer.totalBytes / transferTime;
            
            // Show completion status with speed
            showTransferComplete(finalSpeed);
        }
        
        // CRITICAL FIX: Update receiving transfer status to show completion
        // This ensures the receiving side's UI is properly synchronized
        if (fileTransfer.receiving) {
            elements.transferStatus.textContent = 'Complete';
            elements.progressBar.style.width = '100%';
            elements.progressBar.className = 'bg-green-500 h-3 rounded-full transition-all duration-300';
            
            // Update bytes transferred to show full completion
            if (elements.bytesTransferred && fileTransfer.totalBytes) {
                elements.bytesTransferred.textContent = `${formatBytes(fileTransfer.totalBytes)} / ${formatBytes(fileTransfer.totalBytes)}`;
            }
            
            // Update time remaining to show completion
            if (elements.timeRemaining) {
                elements.timeRemaining.textContent = '00:00';
            }
        }
    };
    
    fileTransfer.onError = (error) => {
        // Handle multiple transfer errors - find the transfer that failed
        const failedTransfer = Array.from(fileTransfer.activeTransfers.values())
            .find(t => t.transferCancelled || (t.sending && error.message.includes('cancelled')));
        
        if (failedTransfer) {
            // Update progress indicator to show error
            const progressElement = activeProgressIndicators.get(failedTransfer.id);
            if (progressElement) {
                const progressBar = progressElement.querySelector('.bg-blue-500');
                const statusElement = progressElement.querySelector('.text-gray-600');
                
                progressBar.style.width = '100%';
                progressBar.className = 'bg-red-500 h-3 rounded-full transition-all duration-300';
                statusElement.textContent = `Error: ${error.message}`;
                
                // Remove cancel button
                const cancelButton = progressElement.querySelector('button');
                if (cancelButton) {
                    cancelButton.remove();
                }
                
                // Remove progress indicator after delay
                setTimeout(() => {
                    removeProgressIndicator(failedTransfer.id);
                }, 3000);
            }
        }
        
        // Show error completion status for legacy compatibility
        showTransferError(error.message);
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
        
        // CRITICAL FIX: Ensure receiving side shows complete status
        // Update progress bar to show completion
        elements.progressBar.style.width = '100%';
        elements.progressBar.className = 'bg-green-500 h-3 rounded-full transition-all duration-300';
        
        // Update bytes transferred to show full completion
        if (elements.bytesTransferred && fileTransfer.totalBytes) {
            elements.bytesTransferred.textContent = `${formatBytes(fileTransfer.totalBytes)} / ${formatBytes(fileTransfer.totalBytes)}`;
        }
        
        // Update time remaining to show completion
        if (elements.timeRemaining) {
            elements.timeRemaining.textContent = '00:00';
        }
        
        // Store blob and file info for potential manual download
        window.receivedFileBlob = blob;
        window.receivedFileInfo = fileInfo;
        
        // Calculate final transfer speed
        const transferTime = (Date.now() - fileTransfer.startTime) / 1000; // seconds
        const finalSpeed = fileTransfer.totalBytes / transferTime;
        
        // Create download URL for history
        const downloadUrl = URL.createObjectURL(blob);
        
        // Add to transfer history
        addToHistory({
            type: 'received',
            filename: fileInfo.name,
            filesize: formatBytes(fileInfo.size),
            speed: formatBytes(finalSpeed) + '/s',
            downloadUrl: downloadUrl
        });
        
        // Try to auto-download first
        try {
            const a = document.createElement('a');
            a.href = downloadUrl;
            a.download = fileInfo.name;
            a.style.display = 'none';
            document.body.appendChild(a);
            a.click();
            
            // Clean up
            setTimeout(() => {
                document.body.removeChild(a);
                // Don't revoke URL here as it's needed for history download
            }, 100);
            
            // Auto-download successful, hide download section
            elements.downloadSection.classList.add('hidden');
        } catch (error) {
            logger.warn('Auto-download failed, showing manual download option:', error);
            // Show manual download option
            elements.downloadSection.classList.remove('hidden');
        }
    };
    
    fileTransfer.onVerificationFailed = (reason) => {
        elements.transferStatus.textContent = `Verification failed: ${reason}`;
    };
    
    // Connect to server button
    elements.connectButton.addEventListener('click', async () => {
        try {
            // Defensive validation: ensure server URL is available
            if (!elements.serverUrl.value || elements.serverUrl.value.trim() === '') {
                logger.error('Server URL is required for connection');
                return;
            }
            
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
    
    // Show transfer completion status
    function showTransferComplete(finalSpeed) {
        const filename = elements.transferFilename.textContent;
        const filesize = elements.bytesTransferred.textContent;
        const speed = finalSpeed ? formatBytes(finalSpeed) + '/s' : elements.transferSpeed.textContent;
        
        // Update completion details
        elements.completionFilename.textContent = filename;
        elements.completionFilesize.textContent = filesize;
        elements.completionSpeed.textContent = speed;
        elements.completionVerification.textContent = '✓ Verified';
        elements.completionVerification.className = 'text-green-600 font-medium';
        
        // Hide progress section and show completion section
        elements.transferProgress.classList.add('hidden');
        elements.transferCompletion.classList.remove('hidden');
        
        // Add to transfer history for sent files
        if (fileTransfer.sending) {
            addToHistory({
                type: 'sent',
                filename: filename,
                filesize: filesize,
                speed: speed,
                downloadUrl: null
            });
        }
        
        // Reset file input after a delay
        setTimeout(() => {
            elements.fileInput.value = '';
            elements.selectedFileName.textContent = 'No file selected';
            elements.sendFileButton.disabled = true;
            
            // Hide completion section after 10 seconds
            setTimeout(() => {
                elements.transferCompletion.classList.add('hidden');
            }, 10000);
        }, 3000);
    }
    
    // Show transfer error status
    function showTransferError(errorMessage) {
        const filename = elements.transferFilename.textContent;
        const filesize = elements.bytesTransferred.textContent;
        const speed = elements.transferSpeed.textContent;
        
        // Update completion details with error
        elements.completionFilename.textContent = filename;
        elements.completionFilesize.textContent = filesize;
        elements.completionSpeed.textContent = speed;
        elements.completionVerification.textContent = `✗ Failed: ${errorMessage}`;
        elements.completionVerification.className = 'text-red-600 font-medium';
        
        // Hide progress section and show completion section
        elements.transferProgress.classList.add('hidden');
        elements.transferCompletion.classList.remove('hidden');
        
        // Add failed transfer to history
        addToHistory({
            type: fileTransfer.sending ? 'sent' : 'received',
            filename: filename,
            filesize: filesize,
            speed: speed,
            downloadUrl: null,
            error: errorMessage
        });
        
        // Reset file input after a delay
        setTimeout(() => {
            elements.fileInput.value = '';
            elements.selectedFileName.textContent = 'No file selected';
            elements.sendFileButton.disabled = true;
            
            // Hide completion section after 10 seconds
            setTimeout(() => {
                elements.transferCompletion.classList.add('hidden');
            }, 10000);
        }, 3000);
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
    
    // Download button click handler
    elements.downloadButton.addEventListener('click', () => {
        if (window.receivedFileBlob && window.receivedFileInfo) {
            try {
                // Use the stored download URL from history if available
                let downloadUrl = null;
                const historyItem = transferHistory.find(item =>
                    item.type === 'received' && item.filename === window.receivedFileInfo.name
                );
                
                if (historyItem && historyItem.downloadUrl) {
                    downloadUrl = historyItem.downloadUrl;
                } else {
                    // Create new URL if not found in history
                    downloadUrl = URL.createObjectURL(window.receivedFileBlob);
                }
                
                const a = document.createElement('a');
                a.href = downloadUrl;
                a.download = window.receivedFileInfo.name;
                a.style.display = 'none';
                document.body.appendChild(a);
                a.click();
                
                // Clean up
                setTimeout(() => {
                    document.body.removeChild(a);
                    // Don't revoke URL if it's from history (needed for future downloads)
                    if (!historyItem || !historyItem.downloadUrl) {
                        URL.revokeObjectURL(downloadUrl);
                    }
                }, 100);
                
                logger.log('Manual download initiated successfully');
            } catch (error) {
                logger.error('Error during manual download:', error);
            }
        } else {
            logger.error('No file data available for download');
        }
    });
    
    // Auto-detect and set server URL FIRST (moved up to fix race condition)
    const detectedServerUrl = detectServerUrl();
    elements.serverUrl.value = detectedServerUrl;
    logger.log(`Auto-detected server URL: ${detectedServerUrl}`);
    
    /**
     * Update connection status display with dual status indicators
     * @param {string} status - The status message
     * @private
     */
    function _updateConnectionStatus(status) {
        // Update main status display
        elements.connectionStatus.textContent = status;
        
        // Update connection indicators
        _updateConnectionIndicators();
    }
    
    /**
     * Update connection indicators for server and P2P connections
     * @private
     */
    function _updateConnectionIndicators() {
        const serverIndicator = document.getElementById('server-status-indicator');
        const p2pIndicator = document.getElementById('p2p-status-indicator');
        
        if (!serverIndicator || !p2pIndicator) {
            return; // Indicators not available yet
        }
        
        // Update server connection indicator
        if (p2p.isServerConnected()) {
            serverIndicator.className = 'inline-flex items-center px-2 py-1 rounded-full text-xs font-medium bg-green-100 text-green-800';
            serverIndicator.innerHTML = `
                <svg class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 20 20">
                    <circle cx="10" cy="10" r="3"></circle>
                </svg>
                Server: Connected
            `;
        } else if (p2p.serverDisconnected) {
            serverIndicator.className = 'inline-flex items-center px-2 py-1 rounded-full text-xs font-medium bg-gray-100 text-gray-800';
            serverIndicator.innerHTML = `
                <svg class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 20 20">
                    <circle cx="10" cy="10" r="3"></circle>
                </svg>
                Server: Disconnected
            `;
        } else {
            serverIndicator.className = 'inline-flex items-center px-2 py-1 rounded-full text-xs font-medium bg-red-100 text-red-800';
            serverIndicator.innerHTML = `
                <svg class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 20 20">
                    <circle cx="10" cy="10" r="3"></circle>
                </svg>
                Server: Not Connected
            `;
        }
        
        // Update P2P connection indicator
        if (p2p.isConnected()) {
            p2pIndicator.className = 'inline-flex items-center px-2 py-1 rounded-full text-xs font-medium bg-green-100 text-green-800';
            p2pIndicator.innerHTML = `
                <svg class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 20 20">
                    <circle cx="10" cy="10" r="3"></circle>
                </svg>
                P2P: Connected
            `;
        } else {
            p2pIndicator.className = 'inline-flex items-center px-2 py-1 rounded-full text-xs font-medium bg-red-100 text-red-800';
            p2pIndicator.innerHTML = `
                <svg class="w-3 h-3 mr-1" fill="currentColor" viewBox="0 0 20 20">
                    <circle cx="10" cy="10" r="3"></circle>
                </svg>
                P2P: Not Connected
            `;
        }
    }
    
    // Check for connection token in URL
    function checkUrlForToken() {
        const urlParams = new URLSearchParams(window.location.search);
        const token = urlParams.get('token');
        
        if (token) {
            // Validate server URL is available before proceeding
            if (!elements.serverUrl.value || elements.serverUrl.value.trim() === '') {
                logger.error('Server URL not available for auto-connection');
                return;
            }
            
            // Set peer token
            elements.peerToken.value = token;
            
            // Auto-connect if token is present (now server URL is guaranteed to be set)
            logger.log('Found token in URL, auto-connecting...');
            elements.connectButton.click();
        }
    }
    
    // Initialize URL token check AFTER server URL is set
    checkUrlForToken();
    
    // Set status log panel to collapsed by default
    const statusLogContent = document.getElementById('status-log-panel-content');
    const statusLogIcon = document.getElementById('status-log-panel-toggle-icon');
    const statusLogPanel = document.getElementById('status-log-panel');
    
    // Ensure status log panel is collapsed (it has 'hidden' class in HTML)
    if (statusLogContent.classList.contains('hidden')) {
        statusLogContent.classList.remove('hidden');
        statusLogContent.classList.add('collapsed');
        statusLogIcon.classList.add('rotate-180');
        statusLogPanel.classList.add('collapsed');
    }
    
    // Set initial expanded state for connection panel
    const connectionContent = document.getElementById('connection-panel-content');
    const connectionIcon = document.getElementById('connection-panel-toggle-icon');
    const connectionPanel = document.getElementById('connection-panel');
    
    connectionContent.classList.add('expanded');
    connectionIcon.classList.remove('rotate-180');
    connectionPanel.classList.remove('collapsed');
}

// Check if DOM is already loaded
if (document.readyState === 'loading') {
    // DOM is still loading, wait for DOMContentLoaded event
    document.addEventListener('DOMContentLoaded', initUI);
} else {
    // DOM is already loaded, initialize immediately
    initUI();
}