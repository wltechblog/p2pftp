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
        this.minWindowSize = 16; // Minimum window size
        this.maxWindowSize = 256; // Maximum window size
        this.inFlightChunks = 0;
        this.lastAcknowledged = -1;
        
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
        this.bufferThreshold = this.p2p.DATA_BUFFER_SIZE || (512 * 1024); // Use configured data buffer size, reduced to 512KB
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
        
        // Wait for capabilities exchange to complete
        try {
            await this.p2p.waitForCapabilitiesExchange();
        } catch (error) {
            throw new Error('Capabilities exchange failed: ' + error.message);
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
            
            // Check if we need to wait for window space with timeout protection
            if (this.inFlightChunks >= this.windowSize) {
                const waitStartTime = Date.now();
                const timeoutMs = 10000; // 10 second timeout
                
                await new Promise(resolve => {
                    const checkWindow = () => {
                        if (this.inFlightChunks < this.windowSize) {
                            resolve();
                        } else if (Date.now() - waitStartTime > timeoutMs) {
                            // CRITICAL FIX: Force window reset if deadlock detected
                            this.logger.warn('Window wait timeout detected, forcing window reset');
                            this.inFlightChunks = Math.floor(this.windowSize / 2);
                            this.lastAcknowledged = Math.max(0, this.lastAcknowledged - Math.floor(this.windowSize / 2));
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
     * Send immediate flow control acknowledgment (unthrottled)
     * @private
     */
    _sendFlowControlAck() {
        const message = {
            type: 'flow-control-ack',
            highestSequence: this.highestSequence
        };
        
        this.p2p.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent flow control acknowledgment:', message);
    }

    /**
     * Handle flow control acknowledgment from peer
     * @param {Object} ack - The flow control acknowledgment
     * @private
     */
    _handleFlowControlAck(ack) {
        if (!this.sending) {
            return;
        }
        
        this.logger.log('Received flow control acknowledgment:', ack);
        
        // Update window control immediately (unthrottled)
        if (ack.highestSequence > this.lastAcknowledged) {
            const newAcknowledged = ack.highestSequence - this.lastAcknowledged;
            this.inFlightChunks = Math.max(0, this.inFlightChunks - newAcknowledged);
            this.lastAcknowledged = ack.highestSequence;
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