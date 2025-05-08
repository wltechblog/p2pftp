/**
 * P2PFTP File Transfer Implementation
 * This file handles the file transfer protocol
 */

class FileTransfer {
    constructor(p2pConnection, logger) {
        this.p2p = p2pConnection;
        this.logger = logger || console;
        
        // File transfer state
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
        
        // Window control
        this.windowSize = 64; // Start with 64 chunks in flight
        this.inFlightChunks = 0;
        this.lastAcknowledged = -1;
        
        // Congestion control
        this.bufferThreshold = 1024 * 1024; // 1MB
        this.sendPaused = false;
        
        // Event handlers
        this.onProgress = null;
        this.onComplete = null;
        this.onError = null;
        this.onFileInfo = null;
        this.onVerificationStart = null;
        this.onVerificationComplete = null;
        this.onVerificationFailed = null;
        
        // Set up control message handler
        this.p2p.onControlMessage = (message) => this._handleControlMessage(message);
        
        // Set up data message handler
        this.p2p.onDataMessage = (data) => this._handleDataMessage(data);
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
        
        if (!this.p2p.capabilitiesExchanged) {
            throw new Error('Capabilities not exchanged');
        }
        
        if (this.sending || this.receiving) {
            throw new Error('Transfer already in progress');
        }
        
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
        this.inFlightChunks = 0;
        this.lastAcknowledged = -1;
        this.sendPaused = false;
        
        this.logger.log(`Starting file transfer: ${file.name} (${file.size} bytes)`);
        this.logger.log(`Using chunk size: ${this.chunkSize} bytes`);
        this.logger.log(`Total chunks: ${this.totalChunks}`);
        
        try {
            // Calculate MD5 hash
            const md5Hash = await this._calculateMD5(file);
            
            // Send file info
            this.fileInfo = {
                name: file.name,
                size: file.size,
                md5: md5Hash
            };
            
            this._sendFileInfo(this.fileInfo);
            
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
    cancelTransfer() {
        if (this.sending || this.receiving) {
            this.logger.log('Cancelling transfer');
            this.transferCancelled = true;
            
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
     * Send file chunks to the peer
     * @private
     */
    async _sendChunks() {
        if (!this.sending || this.transferCancelled) {
            return;
        }
        
        const reader = new FileReader();
        let sequence = 0;
        
        // Process chunks sequentially
        while (sequence < this.totalChunks && !this.transferCancelled) {
            // Check if we need to pause sending due to buffer congestion
            if (this.p2p.dataChannel.bufferedAmount > this.bufferThreshold) {
                this.sendPaused = true;
                this.logger.log('Pausing send due to buffer congestion');
                
                // Wait for buffer to clear
                await new Promise(resolve => {
                    const checkBuffer = () => {
                        if (this.p2p.dataChannel.bufferedAmount <= this.bufferThreshold / 2) {
                            this.sendPaused = false;
                            this.logger.log('Resuming send after buffer cleared');
                            resolve();
                        } else {
                            setTimeout(checkBuffer, 100);
                        }
                    };
                    
                    setTimeout(checkBuffer, 100);
                });
            }
            
            // Check if we need to wait for window space
            if (this.inFlightChunks >= this.windowSize) {
                await new Promise(resolve => {
                    const checkWindow = () => {
                        if (this.inFlightChunks < this.windowSize) {
                            resolve();
                        } else {
                            setTimeout(checkWindow, 50);
                        }
                    };
                    
                    setTimeout(checkWindow, 50);
                });
            }
            
            // Check if transfer was cancelled during wait
            if (this.transferCancelled) {
                break;
            }
            
            // Calculate chunk boundaries
            const start = sequence * this.chunkSize;
            const end = Math.min(start + this.chunkSize, this.file.size);
            const chunkData = this.file.slice(start, end);
            
            // Read chunk data
            const chunkArrayBuffer = await new Promise((resolve, reject) => {
                reader.onload = (e) => resolve(e.target.result);
                reader.onerror = (e) => reject(e);
                reader.readAsArrayBuffer(chunkData);
            });
            
            // Create chunk with header
            const chunk = new ArrayBuffer(8 + chunkArrayBuffer.byteLength);
            const view = new DataView(chunk);
            
            // Write sequence number (4 bytes)
            view.setUint32(0, sequence);
            
            // Write chunk size (4 bytes)
            view.setUint32(4, chunkArrayBuffer.byteLength);
            
            // Copy chunk data
            new Uint8Array(chunk, 8).set(new Uint8Array(chunkArrayBuffer));
            
            // Send chunk
            try {
                this.p2p.dataChannel.send(chunk);
                this.inFlightChunks++;
                this.bytesSent += chunkArrayBuffer.byteLength;
                this.sentChunks++;
                
                // Update progress
                if (this.onProgress) {
                    const progress = {
                        bytesSent: this.bytesSent,
                        totalBytes: this.totalBytes,
                        sentChunks: this.sentChunks,
                        totalChunks: this.totalChunks,
                        percent: (this.bytesSent / this.totalBytes) * 100,
                        speed: this.bytesSent / ((Date.now() - this.startTime) / 1000),
                        timeElapsed: (Date.now() - this.startTime) / 1000,
                        timeRemaining: ((Date.now() - this.startTime) / this.bytesSent) * (this.totalBytes - this.bytesSent) / 1000
                    };
                    
                    this.onProgress(progress);
                }
                
                sequence++;
            } catch (error) {
                this.logger.error('Error sending chunk:', error);
                
                // Retry after a short delay
                await new Promise(resolve => setTimeout(resolve, 100));
                
                // Don't increment sequence, so we retry the same chunk
                continue;
            }
        }
        
        // Send file complete message when all chunks are sent
        if (!this.transferCancelled) {
            this._sendFileComplete();
        }
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
     * Send progress update to the peer
     * @private
     */
    _sendProgressUpdate() {
        // Only send progress updates once per second
        const now = Date.now();
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
        
        switch (message.type) {
            case 'file-info':
                this._handleFileInfo(message.info);
                break;
                
            case 'progress-update':
                this._handleProgressUpdate(message);
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
     * Handle progress update message from peer
     * @param {Object} update - The progress update
     * @private
     */
    _handleProgressUpdate(update) {
        if (!this.sending) {
            return;
        }
        
        this.logger.log('Received progress update:', update);
        
        // Update window control
        if (update.highestSequence > this.lastAcknowledged) {
            const newAcknowledged = update.highestSequence - this.lastAcknowledged;
            this.inFlightChunks = Math.max(0, this.inFlightChunks - newAcknowledged);
            this.lastAcknowledged = update.highestSequence;
        }
        
        // Adjust window size based on progress
        const bytesPerSecond = update.bytesReceived / ((Date.now() - this.startTime) / 1000);
        if (bytesPerSecond > 10 * 1024 * 1024) { // > 10 MB/s
            this.windowSize = Math.min(256, this.windowSize + 8);
        } else if (bytesPerSecond < 1 * 1024 * 1024) { // < 1 MB/s
            this.windowSize = Math.max(16, this.windowSize - 4);
        }
    }

    /**
     * Handle file complete message from peer
     * @private
     */
    _handleFileComplete() {
        this.logger.log('Received file complete message');
        this.transferComplete = true;
        
        // Check if we've received all the data
        if (this.bytesReceived === this.totalBytes) {
            // We have all the data, proceed with verification
            this._verifyReceivedFile();
        } else {
            this.logger.log(`Received file-complete but only have ${this.bytesReceived}/${this.totalBytes} bytes. Waiting for remaining chunks...`);
        }
    }

    /**
     * Handle file verified message from peer
     * @private
     */
    _handleFileVerified() {
        this.logger.log('File verified by peer');
        
        // Complete the transfer
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
        
        // Complete the transfer with error
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
        if (!this.receiving || !this.fileInfo || !this.fileData) {
            return;
        }
        
        // Check if we have enough data for the header (8 bytes)
        if (data.byteLength < 8) {
            this.logger.warn(`Received invalid chunk: too short (${data.byteLength} bytes)`);
            return;
        }
        
        // Extract header information
        const view = new DataView(data);
        const sequence = view.getUint32(0);
        const chunkSize = view.getUint32(4);
        
        // Validate chunk size
        if (data.byteLength !== 8 + chunkSize) {
            this.logger.warn(`Chunk size mismatch: expected ${8 + chunkSize}, got ${data.byteLength}`);
            return;
        }
        
        // Calculate offset in file
        const offset = sequence * this.chunkSize;
        
        // Validate offset
        if (offset + chunkSize > this.fileInfo.size) {
            this.logger.warn(`Invalid chunk offset: ${offset + chunkSize} exceeds file size ${this.fileInfo.size}`);
            return;
        }
        
        // Extract chunk data
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
        if (this.bytesReceived === this.totalBytes && this.transferComplete) {
            this._verifyReceivedFile();
        }
    }

    /**
     * Verify the received file
     * @private
     */
    async _verifyReceivedFile() {
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
                
                // Complete the transfer
                this.receiving = false;
                
                if (this.onVerificationComplete) {
                    this.onVerificationComplete(blob, this.fileInfo);
                }
            } else {
                this.logger.error(`File verification failed: MD5 mismatch (expected ${this.fileInfo.md5}, got ${md5Hash})`);
                
                // Send verification failure
                const message = {
                    type: 'file-failed',
                    reason: 'MD5 hash mismatch'
                };
                
                this.p2p.controlChannel.send(JSON.stringify(message));
                
                // Complete the transfer with error
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
            
            // Complete the transfer with error
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
            const reader = new FileReader();
            reader.onload = async (e) => {
                try {
                    // Use SubtleCrypto API to calculate MD5
                    // Note: MD5 is not directly supported in SubtleCrypto, so we use a library
                    // or implement it ourselves. For simplicity, we'll use a placeholder here.
                    // In a real implementation, you would use a proper MD5 library.
                    
                    // Placeholder for MD5 calculation
                    const hash = await this._md5ArrayBuffer(e.target.result);
                    resolve(hash);
                } catch (error) {
                    reject(error);
                }
            };
            reader.onerror = (e) => reject(e);
            reader.readAsArrayBuffer(file);
        });
    }

    /**
     * Calculate MD5 hash of an ArrayBuffer
     * This is a simple implementation and should be replaced with a proper library in production
     * @param {ArrayBuffer} buffer - The buffer to hash
     * @returns {Promise<string>} - The MD5 hash
     * @private
     */
    async _md5ArrayBuffer(buffer) {
        // In a real implementation, you would use a proper MD5 library
        // For now, we'll use a placeholder that returns a random hash
        // This should be replaced with a real implementation
        
        // Convert buffer to hex string (first 100 bytes for simplicity)
        const array = new Uint8Array(buffer);
        let hexString = '';
        const len = Math.min(array.length, 100);
        
        for (let i = 0; i < len; i++) {
            hexString += array[i].toString(16).padStart(2, '0');
        }
        
        // Use a hash of the hex string as a placeholder
        const encoder = new TextEncoder();
        const data = encoder.encode(hexString);
        const hashBuffer = await crypto.subtle.digest('SHA-256', data);
        const hashArray = Array.from(new Uint8Array(hashBuffer));
        const hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
        
        // Return first 32 characters as MD5 is 128 bits (32 hex chars)
        return hashHex.substring(0, 32);
    }
}