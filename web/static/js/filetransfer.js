/**
 * P2PFTP File Transfer Implementation
 * This file handles the file transfer protocol
 */

class FileTransfer {
    constructor(p2pConnection, logger) {
        this.p2p = p2pConnection;
        this.logger = logger || console;
        
        // Multiple transfer support
        this.activeTransfers = new Map(); // transferId -> transferData
        this.nextTransferId = 1;
        
        // Legacy single transfer state (for backward compatibility)
        this.file = null;
        this.fileInfo = null;
        this.fileData = null;
        this.sending = false;
        this.receiving = false;
        this.transferComplete = false;
        this.bytesReceived = 0;
        this.bytesSent = 0;
        this.totalBytes = 0;
        this.startTime = 0;
        this.highestSequence = 0;
        this.chunks = [];
        this.chunkSize = p2pConnection.DEFAULT_CHUNK_SIZE;
        this.totalChunks = 0;
        this.sentChunks = 0;
        this.receivedChunks = 0;
        this.lastProgressUpdate = 0;
        this.transferCancelled = false;
        
        // Legacy window control - REMOVED for multiple transfer support
        // Per-transfer flow control is now handled in transferData objects
        this.windowSize = 128; // Default values for new transfers
        this.minWindowSize = 32;
        this.maxWindowSize = 512;
        // this.inFlightChunks = 0; // REMOVED - now per-transfer
        // this.lastAcknowledged = -1; // REMOVED - now per-transfer
        
        // Adaptive window control
        this.lastWindowAdjustment = Date.now();
        this.windowAdjustmentInterval = 1000; // Adjust window every 1 second
        this.lastTransferSpeed = 0;
        this.speedHistory = []; // Track transfer speed history
        this.maxSpeedHistory = 10; // Keep last 10 speed measurements
        this.consecutiveIncreases = 0;
        this.consecutiveDecreases = 0;
        
        // Congestion control
        // CRITICAL FIX: Reduced buffer threshold from 2MB to 512KB for better flow control
        // This triggers buffer management earlier and prevents saturation
        this.bufferThreshold = this.p2p.DATA_BUFFER_SIZE || (512 * 1024); // Use configured data buffer size, reduced to 512KB for better flow control
        this.sendPaused = false;
        
        // Speed calculation tracking
        this.lastSpeedCalculationTime = 0;
        this.lastBytesReceived = 0;
        
        // Event handlers
        this.onProgress = null;
        this.onComplete = null;
        this.onError = null;
        this.onFileInfo = null;
        this.onVerificationStart = null;
        this.onVerificationComplete = null;
        this.onVerificationFailed = null;
        
        // Set up control message handler
        this.p2p.onControlMessage = (message) => {
            if (message.type === 'flow-control-ack') {
                this.logger.log('DEBUG: FileTransfer received flow-control-ack:', message);
            }
            this._handleControlMessage(message);
        };
        
        // Set up data message handler
        this.p2p.onDataMessage = (data) => this._handleDataMessage(data);
        
        this.logger.log('DEBUG: FileTransfer callbacks set up. p2p object:', this.p2p ? 'exists' : 'null');
    }

    /**
     * Extract numeric ID from transfer ID string
     * @param {string} transferId - The transfer ID string (e.g., "send-1" or "recv-send-1")
     * @returns {number} - The numeric ID
     * @private
     */
    _extractTransferIdNumber(transferId) {
        // Extract the numeric part from transfer ID
        // Handles both "send-1" -> 1 and "recv-send-1" -> 1
        const parts = transferId.split('-');
        // Get the last part which should be the number
        if (parts.length >= 2) {
            const lastPart = parts[parts.length - 1];
            const num = parseInt(lastPart);
            if (!isNaN(num)) {
                return num;
            }
        }
        return 0;
    }

    /**
     * Send a file to the peer
     * @param {File} file - The file to send
     * @returns {Promise} - Resolves when the file is sent
     */
    async sendFile(file) {
        if (!this.p2p.isConnected()) {
            throw new Error('Not connected to peer');
        }
        
        // Wait for capabilities exchange to complete
        try {
            await this.p2p.waitForCapabilitiesExchange();
        } catch (error) {
            throw new Error('Capabilities exchange failed: ' + error.message);
        }
        
        // Generate unique transfer ID
        const numericId = this.nextTransferId++;
        const transferId = `send-${numericId}`;
        
        // Create transfer data object
        const transferData = {
            id: transferId,
            numericId: numericId,
            file: file,
            sending: true,
            transferComplete: false,
            transferCancelled: false,
            bytesSent: 0,
            sentChunks: 0,
            startTime: Date.now(),
            chunkSize: this.p2p.negotiatedChunkSize,
            totalBytes: file.size,
            totalChunks: Math.ceil(file.size / this.chunkSize),
            inFlightChunks: 0,
            lastAcknowledged: -1,
            sendPaused: false,
            windowSize: this.windowSize,
            minWindowSize: this.minWindowSize,
            maxWindowSize: this.maxWindowSize,
            consecutiveIncreases: 0,
            consecutiveDecreases: 0,
            lastWindowAdjustment: Date.now(),
            speedHistory: [],
            lastSpeedCalculationTime: 0,
            lastBytesReceived: 0
        };
        
        // Store transfer data
        this.activeTransfers.set(transferId, transferData);
        
        // Update legacy state for backward compatibility
        this.file = file;
        this.sending = true;
        this.transferComplete = false;
        this.transferCancelled = false;
        this.bytesSent = 0;
        this.sentChunks = 0;
        this.startTime = Date.now();
        this.chunkSize = this.p2p.negotiatedChunkSize;
        this.totalBytes = file.size;
        this.totalChunks = Math.ceil(file.size / this.chunkSize);
        // this.inFlightChunks = 0; // REMOVED - now per-transfer
        // this.lastAcknowledged = -1; // REMOVED - now per-transfer
        this.sendPaused = false;
        
        this.logger.log(`Starting file transfer: ${file.name} (${file.size} bytes)`);
        this.logger.log(`Using chunk size: ${this.chunkSize} bytes`);
        this.logger.log(`Total chunks: ${this.totalChunks}`);
        
        try {
            // Calculate MD5 hash
            const md5Hash = await this._calculateMD5(file);
            
            // Send file info with transfer ID
            this.fileInfo = {
                name: file.name,
                size: file.size,
                md5: md5Hash,
                transferId: transferId
            };
            
            this._sendFileInfoForTransfer(this.fileInfo, transferId);
            
            // Start sending chunks
            this._sendChunks();
            
            return new Promise((resolve, reject) => {
                // Set up completion handler
                const originalOnComplete = this.onComplete;
                this.onComplete = () => {
                    if (originalOnComplete) {
                        originalOnComplete();
                    }
                    resolve();
                };
                
                // Set up error handler
                const originalOnError = this.onError;
                this.onError = (error) => {
                    if (originalOnError) {
                        originalOnError(error);
                    }
                    reject(error);
                };
            });
        } catch (error) {
            this.sending = false;
            this.logger.error('Error sending file:', error);
            if (this.onError) {
                this.onError(error);
            }
            throw error;
        }
    }

    /**
     * Cancel the current transfer
     */
    cancelTransfer(transferId = null) {
        if (transferId) {
            // Cancel specific transfer
            const transferData = this.activeTransfers.get(transferId);
            if (transferData) {
                this.logger.log('Cancelling transfer:', transferId);
                transferData.transferCancelled = true;
                
                if (transferData.sending) {
                    transferData.sending = false;
                }
                
                if (transferData.receiving) {
                    transferData.receiving = false;
                    transferData.fileData = null;
                }
                
                // Notify peer about cancellation
                this._sendCancellationForTransfer(transferId);
                
                // Remove from active transfers
                this.activeTransfers.delete(transferId);
                
                if (this.onError) {
                    this.onError(new Error(`Transfer ${transferId} cancelled by user`));
                }
            }
        } else {
            // Legacy cancel all transfers
            if (this.sending || this.receiving) {
                this.logger.log('Cancelling all transfers');
                this.transferCancelled = true;
                
                // Cancel all active transfers
                for (const [tid, tdata] of this.activeTransfers.entries()) {
                    tdata.transferCancelled = true;
                    if (tdata.sending) {
                        tdata.sending = false;
                    }
                    if (tdata.receiving) {
                        tdata.receiving = false;
                        tdata.fileData = null;
                    }
                    this._sendCancellationForTransfer(tid);
                }
                
                if (this.sending) {
                    this.sending = false;
                }
                
                if (this.receiving) {
                    this.receiving = false;
                    this.fileData = null;
                }
                
                // Notify peer about cancellation
                this._sendCancellation();
                
                if (this.onError) {
                    this.onError(new Error('Transfer cancelled by user'));
                }
            }
        }
    }

    /**
     * Send file info to the peer
     * @param {Object} fileInfo - The file info object
     * @private
     */
    _sendFileInfo(fileInfo) {
        const message = {
            type: 'file-info',
            info: fileInfo
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent file info:', fileInfo);
    }
    
    /**
     * Send file info to peer with transfer ID
     * @param {Object} fileInfo - The file info object
     * @param {string} transferId - The transfer ID
     * @private
     */
    _sendFileInfoForTransfer(fileInfo, transferId) {
        const message = {
            type: 'file-info',
            transferId: transferId,
            info: fileInfo
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent file info for transfer:', transferId, fileInfo);
    }

    /**
     * Send file chunks to the peer
     * @private
     */
    async _sendChunks() {
        // Get current sending transfers
        const sendingTransfers = Array.from(this.activeTransfers.values()).filter(t => t.sending && !t.transferComplete);
        
        // Process each sending transfer
        for (const transfer of sendingTransfers) {
            await this._sendChunksForTransfer(transfer);
        }
    }
    
    async _sendChunksForTransfer(transferData) {
        if (!transferData.sending || transferData.transferCancelled) {
            return;
        }
        
        const reader = new FileReader();
        let transferSequence = 0;
        
        // Process chunks sequentially for this transfer
        while (transferSequence < transferData.totalChunks && !transferData.transferCancelled) {
            // Check if we need to pause sending due to buffer congestion
            const currentBufferedAmount = this.p2p.dataChannel ? this.p2p.dataChannel.bufferedAmount : 0;
            
            // OPTIMIZATION: Use less conservative buffer management for better throughput
            if (currentBufferedAmount > this.bufferThreshold) {
                transferData.sendPaused = true;
                this.logger.log(`Buffer congestion for transfer ${transferData.id} - buffered: ${currentBufferedAmount}, threshold: ${this.bufferThreshold}`);
                
                // Wait for buffer to clear - use 75% threshold instead of 50% for better flow
                await new Promise(resolve => {
                    const checkBuffer = () => {
                        const newBufferedAmount = this.p2p.dataChannel ? this.p2p.dataChannel.bufferedAmount : 0;
                        if (newBufferedAmount <= this.bufferThreshold * 0.75) {
                            transferData.sendPaused = false;
                            this.logger.log(`Resuming send for transfer ${transferData.id} - buffered: ${newBufferedAmount}`);
                            resolve();
                        } else {
                            setTimeout(checkBuffer, 50); // Check more frequently
                        }
                    };
                    
                    setTimeout(checkBuffer, 50);
                });
            }
            
            // Check if we need to wait for window space - CRITICAL FIX: No recovery mechanism
            if (transferData.inFlightChunks >= transferData.windowSize) {
                const waitStartTime = Date.now();
                const timeoutMs = 10000; // Increased timeout to 10 seconds for stability
                
                await new Promise(resolve => {
                    const checkWindow = () => {
                        const elapsed = Date.now() - waitStartTime;
                        
                        if (transferData.inFlightChunks < transferData.windowSize) {
                            resolve();
                        } else if (elapsed > timeoutMs) {
                            // CRITICAL FIX: Remove recovery mechanism that reduces in-flight chunks
                            // This was causing conflicts between multiple transfers
                            this.logger.error(`Window wait timeout for transfer ${transferData.id} after ${elapsed}ms - this should not happen`);
                            resolve(); // Continue anyway to avoid deadlock
                        } else {
                            setTimeout(checkWindow, 50);
                        }
                    };
                    
                    setTimeout(checkWindow, 50);
                });
            }
            
            // Check if transfer was cancelled during wait
            if (transferData.transferCancelled) {
                break;
            }
            
            // Calculate chunk boundaries
            const start = transferSequence * transferData.chunkSize;
            const end = Math.min(start + transferData.chunkSize, transferData.file.size);
            const chunkData = transferData.file.slice(start, end);
            
            // Read chunk data
            const chunkArrayBuffer = await new Promise((resolve, reject) => {
                reader.onload = (e) => resolve(e.target.result);
                reader.onerror = (e) => reject(e);
                reader.readAsArrayBuffer(chunkData);
            });
            
            // Create chunk with header including transfer ID
            const chunk = new ArrayBuffer(12 + chunkArrayBuffer.byteLength);
            const view = new DataView(chunk);
            
            // Write transfer ID (4 bytes) - CRITICAL FIX for multiple transfers
            // Use direct string-to-number conversion instead of regex
            const transferIdNum = this._extractTransferIdNumber(transferData.id);
            view.setUint32(0, transferIdNum);
            
            // Write sequence number (4 bytes)
            view.setUint32(4, transferSequence);
            
            // Write chunk size (4 bytes)
            view.setUint32(8, chunkArrayBuffer.byteLength);
            
            // Copy chunk data (skip 12-byte header: 4 bytes transfer ID + 4 bytes sequence + 4 bytes chunk size)
            new Uint8Array(chunk, 12).set(new Uint8Array(chunkArrayBuffer));
            
            // Send chunk
            try {
                this.p2p.dataChannel.send(chunk);
                transferData.inFlightChunks++;
                transferData.bytesSent += chunkArrayBuffer.byteLength;
                transferData.sentChunks++;
                
                // Update legacy state for backward compatibility
                this.bytesSent = transferData.bytesSent;
                this.sentChunks = transferData.sentChunks;
                
                // Update progress
                if (this.onProgress) {
                    const progress = {
                        transferId: transferData.id,
                        bytesSent: transferData.bytesSent,
                        totalBytes: transferData.totalBytes,
                        sentChunks: transferData.sentChunks,
                        totalChunks: transferData.totalChunks,
                        percent: (transferData.bytesSent / transferData.totalBytes) * 100,
                        speed: transferData.bytesSent / ((Date.now() - transferData.startTime) / 1000),
                        timeElapsed: (Date.now() - transferData.startTime) / 1000,
                        timeRemaining: ((Date.now() - transferData.startTime) / transferData.bytesSent) * (transferData.totalBytes - transferData.bytesSent) / 1000,
                        complete: transferData.transferComplete, // CRITICAL FIX: Include complete flag for UI synchronization
                        filename: transferData.file.name
                    };
                    
                    this.onProgress(progress);
                }
                
                transferSequence++;
            } catch (error) {
                this.logger.error(`Error sending chunk for transfer ${transferData.id}:`, error);
                
                // Retry after a short delay
                await new Promise(resolve => setTimeout(resolve, 100));
                
                // Don't increment sequence, so we retry the same chunk
                continue;
            }
        }
        
        // Send file complete message when all chunks are sent
        if (!transferData.transferCancelled) {
            this._sendFileCompleteForTransfer(transferData);
        }
    }

    /**
     * Send file complete message to the peer
     * @private
     */
    _sendFileCompleteForTransfer(transferData) {
        const message = {
            type: 'file-complete',
            transferId: transferData.id
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log(`Sent file complete message for transfer ${transferData.id}`);
        transferData.transferComplete = true;
        
        // Update legacy state for backward compatibility
        this.transferComplete = true;
    }

    /**
     * Send file complete message to the peer
     * @private
     */
    _sendFileComplete() {
        const message = {
            type: 'file-complete'
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent file complete message');
        this.transferComplete = true;
    }

    /**
     * Send cancellation message to the peer
     * @private
     */
    _sendCancellation() {
        const message = {
            type: 'transfer-cancelled'
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent transfer cancellation message');
    }
    
    /**
     * Send cancellation message for specific transfer
     * @param {string} transferId - The transfer ID
     * @private
     */
    _sendCancellationForTransfer(transferId) {
        const transferData = this.activeTransfers.get(transferId);
        const msgTransferId = transferData && transferData.remoteTransferId ? transferData.remoteTransferId : transferId;
        const message = {
            type: 'transfer-cancelled',
            transferId: msgTransferId
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent transfer cancellation message for transfer:', msgTransferId);
    }

    /**
     * Send progress update to the peer
     * @private
     */
    _sendProgressUpdate() {
        const now = Date.now();
        
        // CRITICAL FIX: Separate flow control from progress reporting
        // Send immediate flow control acknowledgment (unthrottled)
        this._sendFlowControlAck();
        
        // Send throttled progress updates only for user feedback
        if (now - this.lastProgressUpdate < 1000) {
            return;
        }
        
        this.lastProgressUpdate = now;
        
        const message = {
            type: 'progress-update',
            bytesReceived: this.bytesReceived,
            highestSequence: this.highestSequence
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent progress update:', message);
    }
    
    /**
     * Send progress update for specific transfer
     * @param {string} transferId - The local transfer ID
     * @param {Object} transferData - The transfer data
     * @private
     */
    _sendProgressUpdateForTransfer(transferId, transferData) {
        const now = Date.now();
        
        // Send transfer-specific flow control acknowledgment (unthrottled)
        this._sendFlowControlAckForTransfer(transferId, transferData);
        
        // Send throttled progress updates only for user feedback
        if (now - transferData.lastProgressUpdate < 1000) {
            return;
        }
        
        transferData.lastProgressUpdate = now;
        
        // Use remoteTransferId if this is a received transfer, otherwise use local ID
        const msgTransferId = transferData.remoteTransferId || transferId;
        
        const message = {
            type: 'progress-update',
            transferId: msgTransferId,
            bytesReceived: transferData.bytesReceived,
            highestSequence: transferData.highestSequence
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent progress update for transfer:', msgTransferId, message);
    }
    
    /**
     * Send flow control acknowledgment for specific transfer
     * @param {string} transferId - The local transfer ID
     * @param {Object} transferData - The transfer data
     * @private
     */
    _sendFlowControlAckForTransfer(transferId, transferData) {
        // Use remoteTransferId if this is a received transfer, otherwise use local ID
        const msgTransferId = transferData.remoteTransferId || transferId;
        
        const message = {
            type: 'flow-control-ack',
            transferId: msgTransferId,
            highestSequence: transferData.highestSequence
        };
        
        if (!this.p2p.controlChannel) {
            this.logger.error('DEBUG: Control channel is not available!');
            return;
        }
        
        if (this.p2p.controlChannel.readyState !== 'open') {
            this.logger.error('DEBUG: Control channel is not open! State:', this.p2p.controlChannel.readyState);
            return;
        }
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('DEBUG: Sent flow control acknowledgment for transfer:', msgTransferId, 'sequence:', transferData.highestSequence);
    }

    /**
     * Handle file complete message for specific transfer
     * @param {string} transferId - The transfer ID from the remote peer
     * @private
     */
    _handleFileCompleteForTransfer(transferId) {
        this.logger.log(`Received file complete message for transfer: ${transferId}`);
        
        // Convert remote transfer ID to local transfer ID (incoming transfers are prefixed with "recv-")
        const localTransferId = `recv-${transferId}`;
        
        // Get transfer data
        const transferData = this.activeTransfers.get(localTransferId);
        if (!transferData) {
            this.logger.warn(`Received file complete for unknown transfer: ${transferId} (local: ${localTransferId})`);
            return;
        }
        
        transferData.transferComplete = true;
        
        // Check if we've received all data
        if (transferData.bytesReceived >= transferData.file.size) {
            // We have all data, proceed with verification
            this._verifyReceivedFileForTransfer(localTransferId, transferData);
        } else {
            this.logger.log(`Received file-complete but only have ${transferData.bytesReceived}/${transferData.file.size} bytes for transfer ${localTransferId}. Waiting for remaining chunks...`);
            
            // Set a timeout to handle missing chunks
            transferData.completionTimeout = setTimeout(() => {
                if (transferData.receiving && transferData.transferComplete) {
                    this.logger.log(`Completion timeout reached for transfer ${localTransferId} with ${transferData.bytesReceived}/${transferData.file.size} bytes. Proceeding with verification...`);
                    this._verifyReceivedFileForTransfer(localTransferId, transferData);
                }
            }, 5000); // 5 second timeout
        }
        
        // Update legacy state for backward compatibility
        this.transferComplete = true;
    }
    
    /**
     * Handle file verified message for specific transfer
     * @param {string} transferId - The transfer ID
     * @private
     */
    _handleFileVerifiedForTransfer(transferId) {
        this.logger.log(`File verified by peer for transfer: ${transferId}`);
        
        // Get transfer data
        const transferData = this.activeTransfers.get(transferId);
        if (!transferData) {
            this.logger.warn(`Received file verified for unknown transfer: ${transferId}`);
            return;
        }
        
        // Store transfer type before resetting
        transferData.wasSending = transferData.sending;
        
        // Complete transfer
        transferData.sending = false;
        
        // Update legacy state for backward compatibility
        this.sending = false;
        
        if (this.onComplete) {
            this.onComplete();
        }
    }
    
    /**
     * Handle file failed message for specific transfer
     * @param {string} transferId - The transfer ID
     * @param {string} reason - The failure reason
     * @private
     */
    _handleFileFailedForTransfer(transferId, reason) {
        this.logger.error(`File verification failed for transfer ${transferId}:`, reason);
        
        // Get transfer data
        const transferData = this.activeTransfers.get(transferId);
        if (!transferData) {
            this.logger.warn(`Received file failed for unknown transfer: ${transferId}`);
            return;
        }
        
        // Store transfer type before resetting
        transferData.wasSending = transferData.sending;
        
        // Complete transfer with error
        transferData.sending = false;
        
        // Update legacy state for backward compatibility
        this.sending = false;
        
        if (this.onError) {
            this.onError(new Error(`File verification failed: ${reason}`));
        }
    }
    
    /**
     * Handle transfer cancelled message for specific transfer
     * @param {string} transferId - The transfer ID
     * @private
     */
    _handleTransferCancelledForTransfer(transferId) {
        this.logger.log(`Transfer cancelled by peer for transfer: ${transferId}`);
        
        let lookupId = transferId;
        if (!this.activeTransfers.has(lookupId)) {
            const prefixedId = `recv-${transferId}`;
            if (this.activeTransfers.has(prefixedId)) {
                lookupId = prefixedId;
            }
        }
        
        const transferData = this.activeTransfers.get(lookupId);
        if (!transferData) {
            this.logger.warn(`Received transfer cancelled for unknown transfer: ${transferId}`);
            return;
        }
        
        if (transferData.sending) {
            transferData.sending = false;
        }
        
        if (transferData.receiving) {
            transferData.receiving = false;
            transferData.fileData = null;
        }
        
        if (this.sending) {
            this.sending = false;
        }
        
        if (this.receiving) {
            this.receiving = false;
            this.fileData = null;
        }
        
        if (this.onError) {
            this.onError(new Error('Transfer cancelled by peer'));
        }
    }

    /**
     * Send immediate flow control acknowledgment (unthrottled)
     * @private
     */
    _sendFlowControlAck() {
        const message = {
            type: 'flow-control-ack',
            highestSequence: this.highestSequence
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('DEBUG: Sent flow control acknowledgment:', message, 'highestSequence:', this.highestSequence);
    }
    
    /**
     * Send immediate flow control acknowledgment for specific sequence
     * @param {number} sequence - The sequence number to acknowledge
     * @private
     */
    _sendImmediateFlowControlAck(sequence) {
        const message = {
            type: 'flow-control-ack',
            highestSequence: sequence
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('DEBUG: Sent immediate flow control ack for sequence:', sequence);
    }

    /**
     * Handle flow control acknowledgment from peer
     * @param {Object} ack - The flow control acknowledgment
     * @private
     */
    _handleFlowControlAck(ack) {
        // CRITICAL FIX: Legacy flow control removed - only handle per-transfer flow control
        // This method is kept for backward compatibility but should not be used for multiple transfers
        
        // Only handle per-transfer flow control - ignore global state
        const sendingTransfers = Array.from(this.activeTransfers.values()).filter(t => t.sending && !t.transferComplete);
        
        for (const transfer of sendingTransfers) {
            if (ack.highestSequence > transfer.lastAcknowledged) {
                const newAcknowledged = ack.highestSequence - transfer.lastAcknowledged;
                transfer.inFlightChunks = Math.max(0, transfer.inFlightChunks - newAcknowledged);
                transfer.lastAcknowledged = ack.highestSequence;
                this.logger.log('DEBUG: Transfer', transfer.id, 'updated - inFlightChunks:', transfer.inFlightChunks, 'lastAcknowledged:', transfer.lastAcknowledged);
            }
        }
    }

    /**
     * Handle control message from peer
     * @param {Object} message - The control message
     * @private
     */
    _handleControlMessage(message) {
        if (typeof message === 'string') {
            try {
                message = JSON.parse(message);
            } catch (error) {
                this.logger.error('Error parsing control message:', error);
                return;
            }
        }
        
        if (!message.type) {
            this.logger.warn('Received control message without type:', message);
            return;
        }
        
        // CRITICAL FIX: Route messages with transfer IDs to transfer-specific handlers
        if (message.transferId) {
            this.logger.log('DEBUG: Routing message with transferId:', message.type, message.transferId);
            switch (message.type) {
                case 'file-info':
                    this._handleFileInfoForTransfer(message.info, message.transferId);
                    break;
                    
                case 'progress-update':
                    this._handleProgressUpdateForTransfer(message);
                    break;
                    
                case 'flow-control-ack':
                    this.logger.log('DEBUG: Calling _handleFlowControlAckForTransfer');
                    this._handleFlowControlAckForTransfer(message);
                    break;
                    
                case 'file-complete':
                    this._handleFileCompleteForTransfer(message.transferId);
                    break;
                    
                case 'file-verified':
                    this._handleFileVerifiedForTransfer(message.transferId);
                    break;
                    
                case 'file-failed':
                    this._handleFileFailedForTransfer(message.transferId, message.reason);
                    break;
                    
                case 'transfer-cancelled':
                    this._handleTransferCancelledForTransfer(message.transferId);
                    break;
                    
                default:
                    this.logger.warn('Unknown control message type with transfer ID:', message.type, message);
                    break;
            }
            return;
        }
        
        // Legacy handlers for messages without transfer IDs
        switch (message.type) {
            case 'file-info':
                this._handleFileInfo(message.info);
                break;
                
            case 'progress-update':
                this._handleProgressUpdate(message);
                break;
                
            case 'flow-control-ack':
                this._handleFlowControlAck(message);
                break;
                
            case 'file-complete':
                this._handleFileComplete();
                break;
                
            case 'file-verified':
                this._handleFileVerified();
                break;
                
            case 'file-failed':
                this._handleFileFailed(message.reason);
                break;
                
            case 'transfer-cancelled':
                this._handleTransferCancelled();
                break;
        }
    }

    /**
     * Handle file info message from peer
     * @param {Object} info - The file info
     * @private
     */
    _handleFileInfo(info) {
        this.logger.log('Received file info:', info);
        
        // Reset state
        this.fileInfo = info;
        this.fileData = new Uint8Array(info.size);
        this.receiving = true;
        this.transferComplete = false;
        this.transferCancelled = false;
        this.bytesReceived = 0;
        this.highestSequence = 0;
        this.receivedChunks = 0;
        this.startTime = Date.now();
        this.chunkSize = this.p2p.negotiatedChunkSize;
        this.totalBytes = info.size;
        this.totalChunks = Math.ceil(info.size / this.chunkSize);
        this.chunks = new Array(this.totalChunks).fill(false);
        
        this.logger.log(`Starting file reception: ${info.name} (${info.size} bytes)`);
        this.logger.log(`Using chunk size: ${this.chunkSize} bytes`);
        this.logger.log(`Total chunks: ${this.totalChunks}`);
        
        // Notify about file info
        if (this.onFileInfo) {
            this.onFileInfo(info);
        }
    }
    
    /**
     * Handle file info message for specific transfer
     * @param {Object} info - The file info
     * @param {string} transferId - The transfer ID
     * @private
     */
    _handleFileInfoForTransfer(info, transferId) {
        this.logger.log('Received file info for transfer:', transferId, info);
        
        // Prefix the incoming transfer ID to avoid collision with our own sending transfers
        const localTransferId = `recv-${transferId}`;
        
        // Extract and store the numeric ID for chunk matching
        const numericId = this._extractTransferIdNumber(transferId);
        
        // Create transfer data object for receiving
        const chunkSize = this.p2p.negotiatedChunkSize;
        const totalChunks = Math.ceil(info.size / chunkSize);
        
        const transferData = {
            id: localTransferId,
            remoteTransferId: transferId,
            numericId: numericId,
            file: info,
            receiving: true,
            transferComplete: false,
            transferCancelled: false,
            bytesReceived: 0,
            receivedChunks: 0,
            startTime: Date.now(),
            chunkSize: chunkSize,
            totalBytes: info.size,
            totalChunks: totalChunks,
            chunks: new Array(totalChunks).fill(false),
            highestSequence: 0,
            fileData: new Uint8Array(info.size),
            lastProgressUpdate: 0
        };
        
        // Store transfer data using local transfer ID
        this.activeTransfers.set(localTransferId, transferData);
        
        // Update legacy state for backward compatibility (for first transfer)
        if (this.activeTransfers.size === 1) {
            this.fileInfo = info;
            this.fileData = transferData.fileData;
            this.receiving = true;
            this.transferComplete = false;
            this.transferCancelled = false;
            this.bytesReceived = 0;
            this.highestSequence = 0;
            this.receivedChunks = 0;
            this.startTime = Date.now();
            this.chunkSize = chunkSize;
            this.totalBytes = info.size;
            this.totalChunks = totalChunks;
            this.chunks = transferData.chunks;
        }
        
        this.logger.log(`Starting file reception for transfer ${transferId}: ${info.name} (${info.size} bytes)`);
        this.logger.log(`Using chunk size: ${chunkSize} bytes`);
        this.logger.log(`Total chunks: ${totalChunks}`);
        
        // Notify about file info
        if (this.onFileInfo) {
            this.onFileInfo(info);
        }
    }
    
    /**
     * Handle progress update message for specific transfer
     * @param {Object} update - The progress update
     * @private
     */
    _handleProgressUpdateForTransfer(update) {
        // Get transfer data
        const transferData = this.activeTransfers.get(update.transferId);
        if (!transferData || !transferData.sending) {
            return;
        }
        
        this.logger.log('Received progress update for transfer:', update.transferId, update);
        
        // Calculate current transfer speed using rolling average
        const currentTime = Date.now();
        let currentSpeed;
        
        if (transferData.lastSpeedCalculationTime === 0) {
            // First calculation - initialize
            transferData.lastSpeedCalculationTime = currentTime;
            transferData.lastBytesReceived = update.bytesReceived;
            currentSpeed = 0;
        } else {
            // Calculate speed based on time delta
            const timeDelta = (currentTime - transferData.lastSpeedCalculationTime) / 1000; // seconds
            const bytesDelta = update.bytesReceived - transferData.lastBytesReceived;
            
            if (timeDelta > 0) {
                currentSpeed = bytesDelta / timeDelta;
            } else {
                currentSpeed = 0;
            }
            
            // Update tracking variables
            transferData.lastSpeedCalculationTime = currentTime;
            transferData.lastBytesReceived = update.bytesReceived;
        }
        
        // Track speed history
        transferData.speedHistory.push(currentSpeed);
        if (transferData.speedHistory.length > this.maxSpeedHistory) {
            transferData.speedHistory.shift();
        }
        
        // Adjust window size based on network conditions
        this._adjustWindowSizeForTransfer(transferData, currentSpeed);
    }
    
    /**
     * Handle flow control acknowledgment for specific transfer
     * @param {Object} ack - The flow control acknowledgment
     * @private
     */
    _handleFlowControlAckForTransfer(ack) {
        this.logger.log('DEBUG: _handleFlowControlAckForTransfer called with ack:', ack);
        
        // Get transfer data
        const transferData = this.activeTransfers.get(ack.transferId);
        
        if (!transferData) {
            this.logger.warn('DEBUG: No transfer data found for ack.transferId:', ack.transferId, 'Active transfers:', Array.from(this.activeTransfers.keys()));
            return;
        }
        
        if (!transferData.sending) {
            this.logger.warn('DEBUG: Transfer', ack.transferId, 'is not in sending state. sending:', transferData.sending);
            return;
        }
        
        this.logger.log('DEBUG: Received flow control acknowledgment for transfer:', ack.transferId, ack);
        this.logger.log('DEBUG: Transfer', transferData.id, 'state before update - inFlightChunks:', transferData.inFlightChunks, 'lastAcknowledged:', transferData.lastAcknowledged);
        
        // Update window control immediately (unthrottled)
        if (ack.highestSequence > transferData.lastAcknowledged) {
            const newAcknowledged = ack.highestSequence - transferData.lastAcknowledged;
            transferData.inFlightChunks = Math.max(0, transferData.inFlightChunks - newAcknowledged);
            transferData.lastAcknowledged = ack.highestSequence;
            this.logger.log('DEBUG: Transfer', transferData.id, 'state after update - inFlightChunks:', transferData.inFlightChunks, 'lastAcknowledged:', transferData.lastAcknowledged);
        }
        
        // CRITICAL FIX: Remove legacy state updates to prevent conflicts
        // Only per-transfer state is now maintained
        // Legacy state updates removed to prevent dual flow control conflicts
    }
    
    /**
     * Adjust window size for specific transfer based on network conditions
     * @param {Object} transferData - The transfer data
     * @param {number} currentSpeed - Current transfer speed in bytes per second
     * @private
     */
    _adjustWindowSizeForTransfer(transferData, currentSpeed) {
        const now = Date.now();
        
        // Only adjust window at specified intervals
        if (now - transferData.lastWindowAdjustment < this.windowAdjustmentInterval) {
            return;
        }
        
        // Add minimum transfer protection - prevent window adjustments in first 5 seconds
        const elapsedSeconds = (now - transferData.startTime) / 1000;
        if (elapsedSeconds < 5) {
            return;
        }
        
        transferData.lastWindowAdjustment = now;
        
        // Calculate average speed from history
        let avgSpeed = 0;
        if (transferData.speedHistory.length > 0) {
            avgSpeed = transferData.speedHistory.reduce((sum, speed) => sum + speed, 0) / transferData.speedHistory.length;
        }
        
        // Calculate speed trend
        let speedTrend = 0;
        if (transferData.speedHistory.length >= 3) {
            const recent = transferData.speedHistory.slice(-3);
            const older = transferData.speedHistory.slice(-6, -3);
            if (older.length > 0) {
                const recentAvg = recent.reduce((sum, speed) => sum + speed, 0) / recent.length;
                const olderAvg = older.reduce((sum, speed) => sum + speed, 0) / older.length;
                speedTrend = (recentAvg - olderAvg) / olderAvg;
            }
        }
        
        // Calculate buffer utilization
        const bufferUtilization = this.p2p.dataChannel ?
            this.p2p.dataChannel.bufferedAmount / this.bufferThreshold : 0;
        
        let newWindowSize = transferData.windowSize;
        
        // Less aggressive adaptive window sizing algorithm
        if (bufferUtilization > 0.9) {
            // Buffer is congested, reduce window size (less aggressive)
            newWindowSize = Math.max(transferData.minWindowSize, Math.floor(transferData.windowSize * 0.9));
            transferData.consecutiveDecreases++;
            transferData.consecutiveIncreases = 0;
            this.logger.log(`Buffer congestion detected for transfer ${transferData.id} (${(bufferUtilization * 100).toFixed(1)}%), reducing window to ${newWindowSize}`);
        } else if (bufferUtilization < 0.3 && speedTrend > 0.15) {
            // Network is performing well, increase window size (more conservative)
            newWindowSize = Math.min(transferData.maxWindowSize, Math.floor(transferData.windowSize * 1.15));
            transferData.consecutiveIncreases++;
            transferData.consecutiveDecreases = 0;
            this.logger.log(`Good network conditions for transfer ${transferData.id} (speed trend: ${(speedTrend * 100).toFixed(1)}%), increasing window to ${newWindowSize}`);
        } else if (avgSpeed > 15 * 1024 * 1024) { // > 15 MB/s (higher threshold)
            // High speed, can increase window (more conservative)
            if (transferData.consecutiveDecreases === 0) {
                newWindowSize = Math.min(transferData.maxWindowSize, transferData.windowSize + 4);
                transferData.consecutiveIncreases++;
            }
        } else if (avgSpeed < 500 * 1024) { // < 0.5 MB/s (lower threshold)
            // Low speed, reduce window (more conservative)
            if (transferData.consecutiveIncreases === 0) {
                newWindowSize = Math.max(transferData.minWindowSize, transferData.windowSize - 2);
                transferData.consecutiveDecreases++;
            }
        }
        
        // Apply new window size if changed
        if (newWindowSize !== transferData.windowSize) {
            this.logger.log(`Window size adjusted for transfer ${transferData.id}: ${transferData.windowSize} -> ${newWindowSize} (avg speed: ${(avgSpeed / 1024 / 1024).toFixed(2)} MB/s, buffer: ${(bufferUtilization * 100).toFixed(1)}%)`);
            transferData.windowSize = newWindowSize;
        }
        
        // Reset counters if we've had consecutive changes in same direction
        if (transferData.consecutiveIncreases >= 3) {
            transferData.consecutiveIncreases = 0;
        }
        if (transferData.consecutiveDecreases >= 3) {
            transferData.consecutiveDecreases = 0;
        }
    }

    /**
     * Handle progress update message from peer
     * @param {Object} update - The progress update
     * @private
     */
    _handleProgressUpdate(update) {
        if (!this.sending) {
            return;
        }
        
        this.logger.log('Received progress update:', update);
        
        // CRITICAL FIX: Flow control is now handled separately by _handleFlowControlAck()
        // This method now only handles speed calculation and window adjustment
        
        // Calculate current transfer speed using rolling average
        const currentTime = Date.now();
        let currentSpeed;
        
        if (this.lastSpeedCalculationTime === 0) {
            // First calculation - initialize
            this.lastSpeedCalculationTime = currentTime;
            this.lastBytesReceived = update.bytesReceived;
            currentSpeed = 0;
        } else {
            // Calculate speed based on time delta
            const timeDelta = (currentTime - this.lastSpeedCalculationTime) / 1000; // seconds
            const bytesDelta = update.bytesReceived - this.lastBytesReceived;
            
            if (timeDelta > 0) {
                currentSpeed = bytesDelta / timeDelta;
            } else {
                currentSpeed = 0;
            }
            
            // Update tracking variables
            this.lastSpeedCalculationTime = currentTime;
            this.lastBytesReceived = update.bytesReceived;
        }
        
        // Track speed history
        this.speedHistory.push(currentSpeed);
        if (this.speedHistory.length > this.maxSpeedHistory) {
            this.speedHistory.shift();
        }
        
        // Adjust window size based on network conditions
        this._adjustWindowSize(currentSpeed);
    }

    /**
     * Handle file complete message from peer
     * @private
     */
    _handleFileComplete() {
        this.logger.log('Received file complete message');
        this.transferComplete = true;
        
        // Check if we've received all the data
        if (this.bytesReceived >= this.totalBytes) {
            // We have all the data, proceed with verification
            this._verifyReceivedFile();
        } else {
            this.logger.log(`Received file-complete but only have ${this.bytesReceived}/${this.totalBytes} bytes. Waiting for remaining chunks...`);
            
            // Set a timeout to handle missing chunks
            this.completionTimeout = setTimeout(() => {
                if (this.receiving && this.transferComplete) {
                    this.logger.log(`Completion timeout reached with ${this.bytesReceived}/${this.totalBytes} bytes. Proceeding with verification...`);
                    this._verifyReceivedFile();
                }
            }, 5000); // 5 second timeout
        }
    }

    /**
     * Handle file verified message from peer
     * @private
     */
    _handleFileVerified() {
        this.logger.log('File verified by peer');
        
        // Complete transfer
        this.sending = false;
        
        if (this.onComplete) {
            this.onComplete();
        }
    }

    /**
     * Handle file failed message from peer
     * @param {string} reason - The failure reason
     * @private
     */
    _handleFileFailed(reason) {
        this.logger.error('File verification failed:', reason);
        
        // Complete transfer with error
        this.sending = false;
        
        if (this.onError) {
            this.onError(new Error(`File verification failed: ${reason}`));
        }
    }

    /**
     * Handle transfer cancelled message from peer
     * @private
     */
    _handleTransferCancelled() {
        this.logger.log('Transfer cancelled by peer');
        
        // Reset state
        if (this.sending) {
            this.sending = false;
        }
        
        if (this.receiving) {
            this.receiving = false;
            this.fileData = null;
        }
        
        if (this.onError) {
            this.onError(new Error('Transfer cancelled by peer'));
        }
    }

    /**
     * Handle data message from peer
     * @param {ArrayBuffer} data - The data message
     * @private
     */
    _handleDataMessage(data) {
        // Check if we have enough data for the header (12 bytes with transfer ID)
        if (data.byteLength < 12) {
            this.logger.warn(`Received invalid chunk: too short (${data.byteLength} bytes)`);
            return;
        }
        
        // CRITICAL FIX: Extract transfer ID from header
        const view = new DataView(data);
        const transferIdNum = view.getUint32(0);
        const sequence = view.getUint32(4);
        const chunkSize = view.getUint32(8);
        
        // Validate chunk size
        if (data.byteLength !== 12 + chunkSize) {
            this.logger.warn(`Chunk size mismatch: expected ${12 + chunkSize}, got ${data.byteLength}`);
            return;
        }
        
        // CRITICAL FIX: Find transfer by numeric ID
        let transferId = null;
        let transferData = null;
        
        for (const [tid, tdata] of this.activeTransfers.entries()) {
            if (!tdata.receiving) {
                continue;
            }
            if (tdata.numericId === transferIdNum) {
                transferId = tid;
                transferData = tdata;
                break;
            }
        }
        
        if (!transferData) {
            // Fallback to legacy handling ONLY for single transfers (not when multiple transfers are active)
            if (this.activeTransfers.size === 1 && this.receiving && this.fileInfo && this.fileData) {
                this._handleLegacyDataMessage(data, view, sequence, chunkSize);
                return;
            }

            this.logger.warn(`Received chunk for unknown or inactive transfer ID: ${transferIdNum}`);
            return;
        }
        
        // CRITICAL FIX: Handle data for specific transfer
        this._handleDataMessageForTransfer(transferId, transferData, data, view, sequence, chunkSize);
    }
    
    /**
     * Handle data message for specific transfer
     * @param {string} transferId - The transfer ID
     * @param {Object} transferData - The transfer data
     * @param {ArrayBuffer} data - The data message
     * @param {DataView} view - The data view
     * @param {number} sequence - The sequence number
     * @param {number} chunkSize - The chunk size
     * @private
     */
    _handleDataMessageForTransfer(transferId, transferData, data, view, sequence, chunkSize) {
        // Calculate offset in file
        const offset = sequence * transferData.chunkSize;
        
        // Validate offset
        if (offset + chunkSize > transferData.file.size) {
            this.logger.warn(`Invalid chunk offset: ${offset + chunkSize} exceeds file size ${transferData.file.size} for transfer ${transferId}`);
            return;
        }
        
        // Extract chunk data
        const chunkData = new Uint8Array(data, 12, chunkSize);
        
        // Write chunk to transfer-specific file data
        transferData.fileData.set(chunkData, offset);
        
        // Mark chunk as received
        if (!transferData.chunks[sequence]) {
            transferData.chunks[sequence] = true;
            transferData.receivedChunks++;
            transferData.bytesReceived += chunkSize;
            
            // Update highest sequence
            if (sequence > transferData.highestSequence) {
                transferData.highestSequence = sequence;
            }
        }
        
        // Send transfer-specific progress update (which includes flow control ack)
        this._sendProgressUpdateForTransfer(transferId, transferData);
        
        // Update progress
        if (this.onProgress) {
            const progress = {
                transferId: transferId,
                bytesReceived: transferData.bytesReceived,
                totalBytes: transferData.totalBytes,
                receivedChunks: transferData.receivedChunks,
                totalChunks: transferData.totalChunks,
                percent: (transferData.bytesReceived / transferData.totalBytes) * 100,
                speed: transferData.bytesReceived / ((Date.now() - transferData.startTime) / 1000),
                timeElapsed: (Date.now() - transferData.startTime) / 1000,
                complete: transferData.transferComplete, // CRITICAL FIX: Include complete flag for UI synchronization
                timeRemaining: ((Date.now() - transferData.startTime) / transferData.bytesReceived) * (transferData.totalBytes - transferData.bytesReceived) / 1000,
                filename: transferData.file.name
            };
            
            this.onProgress(progress);
        }
        
        // Check if we've received all chunks and transfer is complete
        if (transferData.bytesReceived >= transferData.totalBytes && transferData.transferComplete) {
            this._verifyReceivedFileForTransfer(transferId, transferData);
        }
    }
    
    /**
     * Handle legacy data message (for backward compatibility)
     * @param {ArrayBuffer} data - The data message
     * @param {DataView} view - The data view
     * @param {number} sequence - The sequence number
     * @param {number} chunkSize - The chunk size
     * @private
     */
    _handleLegacyDataMessage(data, view, sequence, chunkSize) {
        // Calculate offset in file
        const offset = sequence * this.chunkSize;
        
        // Validate offset
        if (offset + chunkSize > this.fileInfo.size) {
            this.logger.warn(`Invalid chunk offset: ${offset + chunkSize} exceeds file size ${this.fileInfo.size}`);
            return;
        }
        
        // Extract chunk data (skip 8-byte header for legacy format: 4 bytes sequence + 4 bytes chunk size)
        const chunkData = new Uint8Array(data, 8, chunkSize);
        
        // Write chunk to file data
        this.fileData.set(chunkData, offset);
        
        // Mark chunk as received
        if (!this.chunks[sequence]) {
            this.chunks[sequence] = true;
            this.receivedChunks++;
            this.bytesReceived += chunkSize;
            
            // Update highest sequence
            if (sequence > this.highestSequence) {
                this.highestSequence = sequence;
            }
        }
        
        // Send progress update
        this._sendProgressUpdate();
        
        // Send immediate flow control acknowledgment
        this._sendImmediateFlowControlAck(sequence);
        
        // Update progress
        if (this.onProgress) {
            const progress = {
                bytesReceived: this.bytesReceived,
                totalBytes: this.totalBytes,
                receivedChunks: this.receivedChunks,
                totalChunks: this.totalChunks,
                percent: (this.bytesReceived / this.totalBytes) * 100,
                speed: this.bytesReceived / ((Date.now() - this.startTime) / 1000),
                timeElapsed: (Date.now() - this.startTime) / 1000,
                timeRemaining: ((Date.now() - this.startTime) / this.bytesReceived) * (this.totalBytes - this.bytesReceived) / 1000
            };
            
            this.onProgress(progress);
        }
        
        // Check if we've received all chunks and transfer is complete
        if (this.bytesReceived >= this.totalBytes && this.transferComplete) {
            this._verifyReceivedFile();
        }
    }

    /**
     * Verify received file for specific transfer
     * @param {string} transferId - The transfer ID
     * @param {Object} transferData - The transfer data
     * @private
     */
    async _verifyReceivedFileForTransfer(transferId, transferData) {
        // Clear any completion timeout
        if (transferData.completionTimeout) {
            clearTimeout(transferData.completionTimeout);
            transferData.completionTimeout = null;
        }
        
        if (this.onVerificationStart) {
            this.onVerificationStart();
        }
        
        this.logger.log('Verifying file for transfer:', transferId);
        
        try {
            // Create a blob from transfer-specific file data
            const blob = new Blob([transferData.fileData], { type: 'application/octet-stream' });
            
            // Calculate MD5 hash
            const md5Hash = await this._calculateMD5(blob);
            
            // Get file info from transfer data
            const fileInfo = {
                name: transferData.file.name,
                size: transferData.file.size,
                md5: transferData.file.md5
            };
            
            // Compare with expected hash
            if (md5Hash === fileInfo.md5) {
                this.logger.log('File verification successful for transfer:', transferId);
                
                // Use remoteTransferId for the message back to sender
                const msgTransferId = transferData.remoteTransferId || transferId;
                
                // Send verification success
                const message = {
                    type: 'file-verified',
                    transferId: msgTransferId
                };
                
                this.p2p.controlChannel.send(JSON.stringify(message));
                
                // Complete transfer
                transferData.receiving = false;
                
                // Send final progress update to ensure UI is synchronized
                if (this.onProgress) {
                    const progress = {
                        transferId: transferId,
                        bytesReceived: transferData.file.size,
                        totalBytes: transferData.file.size,
                        receivedChunks: transferData.receivedChunks,
                        totalChunks: transferData.totalChunks,
                        percent: 100,
                        speed: transferData.bytesReceived / ((Date.now() - transferData.startTime) / 1000),
                        timeElapsed: (Date.now() - transferData.startTime) / 1000,
                        timeRemaining: 0,
                        complete: true,
                        filename: transferData.file.name
                    };
                    
                    this.onProgress(progress);
                }
                
                if (this.onVerificationComplete) {
                    this.onVerificationComplete(blob, fileInfo);
                }
                
                // Trigger receiver completion callback for UI synchronization
                if (this.onComplete) {
                    this.onComplete();
                }
            } else {
                this.logger.error(`File verification failed for transfer ${transferId}: MD5 mismatch (expected ${fileInfo.md5}, got ${md5Hash})`);
                
                // Use remoteTransferId for the message back to sender
                const msgTransferId = transferData.remoteTransferId || transferId;
                
                // Send verification failure
                const message = {
                    type: 'file-failed',
                    transferId: msgTransferId,
                    reason: 'MD5 hash mismatch'
                };
                
                this.p2p.controlChannel.send(JSON.stringify(message));
                
                // Complete transfer with error
                transferData.receiving = false;
                
                if (this.onVerificationFailed) {
                    this.onVerificationFailed('MD5 hash mismatch');
                }
            }
        } catch (error) {
            this.logger.error('Error verifying file for transfer:', transferId, error);
            
            // Send verification failure
            const message = {
                type: 'file-failed',
                transferId: transferId,
                reason: error.message
            };
            
            this.p2p.controlChannel.send(JSON.stringify(message));
            
            // Complete transfer with error
            transferData.receiving = false;
            
            if (this.onVerificationFailed) {
                this.onVerificationFailed(error.message);
            }
        }
    }

    /**
     * Verify received file
     * @private
     */
    async _verifyReceivedFile() {
        // Clear any completion timeout
        if (this.completionTimeout) {
            clearTimeout(this.completionTimeout);
            this.completionTimeout = null;
        }
        
        if (this.onVerificationStart) {
            this.onVerificationStart();
        }
        
        this.logger.log('Verifying file...');
        
        try {
            // Create a blob from the file data
            const blob = new Blob([this.fileData], { type: 'application/octet-stream' });
            
            // Calculate MD5 hash
            const md5Hash = await this._calculateMD5(blob);
            
            // Compare with expected hash
            if (md5Hash === this.fileInfo.md5) {
                this.logger.log('File verification successful');
                
                // Send verification success
                const message = {
                    type: 'file-verified'
                };
                
                this.p2p.controlChannel.send(JSON.stringify(message));
                
                // Complete transfer
                this.receiving = false;
                
                // Send final progress update to ensure UI is synchronized
                if (this.onProgress) {
                    const progress = {
                        bytesReceived: this.totalBytes,
                        totalBytes: this.totalBytes,
                        receivedChunks: this.receivedChunks,
                        totalChunks: this.totalChunks,
                        percent: 100,
                        speed: this.bytesReceived / ((Date.now() - this.startTime) / 1000),
                        timeElapsed: (Date.now() - this.startTime) / 1000,
                        timeRemaining: 0,
                        complete: true
                    };
                    
                    this.onProgress(progress);
                }
                
                if (this.onVerificationComplete) {
                    this.onVerificationComplete(blob, this.fileInfo);
                }
                
                // Trigger receiver completion callback for UI synchronization
                if (this.onComplete) {
                    this.onComplete();
                }
            } else {
                this.logger.error(`File verification failed: MD5 mismatch (expected ${this.fileInfo.md5}, got ${md5Hash})`);
                
                // Send verification failure
                const message = {
                    type: 'file-failed',
                    reason: 'MD5 hash mismatch'
                };
                
                this.p2p.controlChannel.send(JSON.stringify(message));
                
                // Complete transfer with error
                this.receiving = false;
                
                if (this.onVerificationFailed) {
                    this.onVerificationFailed('MD5 hash mismatch');
                }
            }
        } catch (error) {
            this.logger.error('Error verifying file:', error);
            
            // Send verification failure
            const message = {
                type: 'file-failed',
                reason: error.message
            };
            
            this.p2p.controlChannel.send(JSON.stringify(message));
            
            // Complete transfer with error
            this.receiving = false;
            
            if (this.onVerificationFailed) {
                this.onVerificationFailed(error.message);
            }
        }
    }

    /**
     * Calculate MD5 hash of a file or blob
     * @param {File|Blob} file - The file or blob to hash
     * @returns {Promise<string>} - The MD5 hash
     * @private
     */
    async _calculateMD5(file) {
        return new Promise((resolve, reject) => {
            if (typeof SparkMD5 === 'undefined') {
                reject(new Error('SparkMD5 library not loaded'));
                return;
            }

            const chunkSize = 2 * 1024 * 1024; // 2MB chunks for large files
            const chunks = Math.ceil(file.size / chunkSize);
            let currentChunk = 0;
            const spark = new SparkMD5.ArrayBuffer();
            const reader = new FileReader();

            reader.onload = (e) => {
                try {
                    spark.append(e.target.result);
                    currentChunk++;

                    if (currentChunk < chunks) {
                        loadNext();
                    } else {
                        // All chunks processed, get the hash
                        const hash = spark.end();
                        resolve(hash);
                    }
                } catch (error) {
                    reject(error);
                }
            };

            reader.onerror = (e) => {
                reject(new Error('Error reading file for MD5 calculation'));
            };

            const loadNext = () => {
                const start = currentChunk * chunkSize;
                const end = Math.min(start + chunkSize, file.size);
                reader.readAsArrayBuffer(file.slice(start, end));
            };

            // Start processing
            loadNext();
        });
    }

    /**
     * Adjust window size based on network conditions and performance
     * @param {number} currentSpeed - Current transfer speed in bytes per second
     * @private
     */
    _adjustWindowSize(currentSpeed) {
        const now = Date.now();
        
        // Only adjust window at specified intervals
        if (now - this.lastWindowAdjustment < this.windowAdjustmentInterval) {
            return;
        }
        
        // Add minimum transfer protection - prevent window adjustments in first 5 seconds
        const elapsedSeconds = (now - this.startTime) / 1000;
        if (elapsedSeconds < 5) {
            return;
        }
        
        this.lastWindowAdjustment = now;
        
        // Calculate average speed from history
        let avgSpeed = 0;
        if (this.speedHistory.length > 0) {
            avgSpeed = this.speedHistory.reduce((sum, speed) => sum + speed, 0) / this.speedHistory.length;
        }
        
        // Calculate speed trend
        let speedTrend = 0;
        if (this.speedHistory.length >= 3) {
            const recent = this.speedHistory.slice(-3);
            const older = this.speedHistory.slice(-6, -3);
            if (older.length > 0) {
                const recentAvg = recent.reduce((sum, speed) => sum + speed, 0) / recent.length;
                const olderAvg = older.reduce((sum, speed) => sum + speed, 0) / older.length;
                speedTrend = (recentAvg - olderAvg) / olderAvg;
            }
        }
        
        // Calculate buffer utilization
        const bufferUtilization = this.p2p.dataChannel ?
            this.p2p.dataChannel.bufferedAmount / this.bufferThreshold : 0;
        
        let newWindowSize = this.windowSize;
        
        // Less aggressive adaptive window sizing algorithm
        if (bufferUtilization > 0.9) {
            // Buffer is congested, reduce window size (less aggressive)
            newWindowSize = Math.max(this.minWindowSize, Math.floor(this.windowSize * 0.9));
            this.consecutiveDecreases++;
            this.consecutiveIncreases = 0;
            this.logger.log(`Buffer congestion detected (${(bufferUtilization * 100).toFixed(1)}%), reducing window to ${newWindowSize}`);
        } else if (bufferUtilization < 0.3 && speedTrend > 0.15) {
            // Network is performing well, increase window size (more conservative)
            newWindowSize = Math.min(this.maxWindowSize, Math.floor(this.windowSize * 1.15));
            this.consecutiveIncreases++;
            this.consecutiveDecreases = 0;
            this.logger.log(`Good network conditions (speed trend: ${(speedTrend * 100).toFixed(1)}%), increasing window to ${newWindowSize}`);
        } else if (avgSpeed > 15 * 1024 * 1024) { // > 15 MB/s (higher threshold)
            // High speed, can increase window (more conservative)
            if (this.consecutiveDecreases === 0) {
                newWindowSize = Math.min(this.maxWindowSize, this.windowSize + 4);
                this.consecutiveIncreases++;
            }
        } else if (avgSpeed < 500 * 1024) { // < 0.5 MB/s (lower threshold)
            // Low speed, reduce window (more conservative)
            if (this.consecutiveIncreases === 0) {
                newWindowSize = Math.max(this.minWindowSize, this.windowSize - 2);
                this.consecutiveDecreases++;
            }
        }
        
        // Apply new window size if changed
        if (newWindowSize !== this.windowSize) {
            this.logger.log(`Window size adjusted: ${this.windowSize} -> ${newWindowSize} (avg speed: ${(avgSpeed / 1024 / 1024).toFixed(2)} MB/s, buffer: ${(bufferUtilization * 100).toFixed(1)}%)`);
            this.windowSize = newWindowSize;
        }
        
        // Reset counters if we've had consecutive changes in same direction
        if (this.consecutiveIncreases >= 3) {
            this.consecutiveIncreases = 0;
        }
        if (this.consecutiveDecreases >= 3) {
            this.consecutiveDecreases = 0;
        }
    }
}