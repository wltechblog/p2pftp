import { CHUNK_SIZE, PROGRESS_UPDATE_INTERVAL, BYTES_PER_SEC_SMOOTHING } from '/static/js/config.js';
import { calculateMD5, formatBytes, showNotification, updateTitleWithSpinner } from '/static/js/utils.js';
import * as ui from '/static/js/ui.js';
import { getDataChannel } from '/static/js/webrtc.js';

// File transfer states
const receiveState = {
    buffer: [],
    receivedSize: 0,
    fileInfo: null,
    startTime: 0,
    lastUpdate: 0,
    bytesPerSecond: 0,
    inProgress: false,
    missingChunks: new Set(),  // Track missing chunks
    lastReceivedSequence: -1,  // Last in-order sequence received
    receivedChunks: new Set(), // Track received chunks
    pendingRetransmissions: new Set(), // Chunks we've requested to be resent
    retransmissionTimer: null  // Timer for requesting missing chunks
};

const sendState = {
    currentFile: null,
    offset: 0,
    startTime: 0,
    lastUpdate: 0,
    lastOffset: 0,
    bytesPerSecond: 0,
    inProgress: false,
    windowSize: 64,           // Number of chunks to send before waiting for acks (increased for better throughput)
    nextSequenceToSend: 0,    // Next sequence number to send
    lastAckedSequence: -1,    // Last sequence number that was acknowledged
    unacknowledgedChunks: {}, // Map of sequence numbers to chunks that haven't been acked
    retransmissionQueue: [],  // Queue of chunks to retransmit
    retransmissionTimer: null, // Timer for retransmissions
    retransmissionTimeout: 3000, // Time in ms before considering a chunk lost
    chunkTimestamps: {},      // Map of sequence numbers to timestamps when they were sent
    congestionWindow: 64,     // Dynamic window size that adjusts based on network conditions
    consecutiveTimeouts: 0    // Track consecutive timeouts for congestion control
};

// Initialize file transfer functionality
export function init() {
    // Check for File System Access API support
    const hasNativeFS = 'showSaveFilePicker' in window;
    if (!hasNativeFS) {
        // Calculate safe memory limit (2GB minus overhead)
        const maxSafeSize = 2 * 1024 * 1024 * 1024 - (100 * 1024 * 1024); // 2GB - 100MB overhead
        ui.addSystemMessage(
            `⚠️ Your browser doesn't support the File System Access API. ` +
            `Large file support is limited to ${formatBytes(maxSafeSize)}. ` +
            `For larger files, please use a modern browser like Chrome or Edge.`
        );
    }
}

// Process a chunk after converting to Uint8Array
async function processChunk(chunk) {
    if (!receiveState.fileInfo || !receiveState.fileInfo.currentChunk) {
        console.error('[WebRTC] Missing chunk metadata');
        return;
    }

    const { sequence, totalChunks: chunksTotal, size } = receiveState.fileInfo.currentChunk;
    console.debug(`[WebRTC] Processing chunk ${sequence + 1}/${chunksTotal}, size: ${size}`);

    // Verify chunk size matches metadata
    if (chunk.byteLength !== size) {
        console.error(`[WebRTC] Chunk size mismatch. Expected: ${size}, Got: ${chunk.byteLength}`);
        return;
    }

    // Verify sequence is within bounds
    if (sequence >= receiveState.buffer.length) {
        console.error(`[WebRTC] Invalid chunk sequence: ${sequence}, total chunks: ${receiveState.buffer.length}`);
        return;
    }

    // Store chunk for immediate writing if we have an active file handle
    if (receiveState.fileHandle && receiveState.fileWriter) {
        // Write chunk directly to file
        try {
            const position = BigInt(sequence) * BigInt(CHUNK_SIZE);
            await receiveState.fileWriter.write({ type: 'write', position: Number(position), data: chunk });
            receiveState.receivedSize += chunk.byteLength;
            receiveState.buffer[sequence] = true; // Mark as received
        } catch (error) {
            console.error('[WebRTC] Error writing chunk:', error);
            ui.showError(`Failed to write chunk: ${error.message}`);
            return;
        }
    } else {
        // Fallback to buffer if no file handle yet
        receiveState.buffer[sequence] = chunk;
        receiveState.receivedSize += chunk.byteLength;
    }

    // Progress and transfer rate tracking
    const now = Date.now();
    const timeDiff = now - receiveState.lastUpdate;
    if (timeDiff >= PROGRESS_UPDATE_INTERVAL) {
        const instantRate = chunk.byteLength / (now - receiveState.lastUpdate) * 1000;
        receiveState.bytesPerSecond = receiveState.bytesPerSecond * (1 - BYTES_PER_SEC_SMOOTHING) + instantRate * BYTES_PER_SEC_SMOOTHING;
        receiveState.lastUpdate = now;
    }

    // Use BigInt for precise calculation with large files
    const received = BigInt(receiveState.receivedSize);
    const total = BigInt(receiveState.fileInfo.size);
    const percentage = Math.min(Number((received * BigInt(100)) / total), 100);
    ui.updateTransferProgress(percentage, `⬇ ${receiveState.fileInfo.name} - ${formatBytes(receiveState.bytesPerSecond)}/s`, "receive");

    // Clear chunk info
    delete receiveState.fileInfo.currentChunk;
}

// Handle incoming control channel messages (JSON)
export async function handleControlMessage(event) {
    const data = event.data;

    if (typeof data === 'string') {
        try {
            const messageObj = JSON.parse(data);

            if (messageObj.type === 'message') {
                ui.addPeerMessage(messageObj.content);
            } else if (messageObj.type === 'file-info') {
                if (receiveState.inProgress) {
                    console.error('[WebRTC] Cannot receive file: Download already in progress');
                    return;
                }

                try {
                    let hasNativeFS = 'showSaveFilePicker' in window;
                    const fileSize = BigInt(messageObj.info.size);
                    const chunkSize = BigInt(CHUNK_SIZE);
                    const numChunks = messageObj.info.totalChunks || Number((fileSize + chunkSize - BigInt(1)) / chunkSize);
                    receiveState.fileInfo = messageObj.info;

                    if (hasNativeFS) {
                        // Use File System Access API if available
                        const handle = await window.showSaveFilePicker({
                            suggestedName: messageObj.info.name,
                            types: [{
                                description: 'File',
                                accept: { '*/*': ['.'] }
                            }],
                        });

                        // Set up file handle and writer
                        receiveState.fileHandle = handle;
                        receiveState.fileWriter = await handle.createWritable();
                        receiveState.buffer = new Array(numChunks).fill(false);
                    } else {
                        // Fallback to in-memory buffer for older browsers
                        receiveState.buffer = new Array(numChunks).fill(null);
                    }

                    // Initialize transfer state
                    receiveState.receivedSize = 0;
                    receiveState.startTime = Date.now();
                    receiveState.lastUpdate = Date.now();
                    receiveState.bytesPerSecond = 0;
                    receiveState.inProgress = true;
                    receiveState.lastReceivedSequence = -1;
                    receiveState.receivedChunks = new Set();
                    receiveState.missingChunks = new Set();
                    receiveState.pendingRetransmissions = new Set();

                    // Clear any existing retransmission timer
                    if (receiveState.retransmissionTimer) {
                        clearInterval(receiveState.retransmissionTimer);
                    }

                    // Set up timer to check for missing chunks (every 1 second for more responsive recovery)
                    receiveState.retransmissionTimer = setInterval(checkForMissingChunks, 1000);

                    ui.addSystemMessage(`Receiving file: ${receiveState.fileInfo.name} (${formatBytes(receiveState.fileInfo.size)})`);
                    ui.updateConnectionStatus(`Receiving file...`);
                    ui.updateTransferProgress(0, `⬇ ${receiveState.fileInfo.name}`, "receive");
                } catch (error) {
                    console.error('[WebRTC] Error setting up file reception:', error);
                    ui.showError(`Failed to set up file reception: ${error.message}`);
                    return;
                }
            } else if (messageObj.type === 'chunk-info') {
                // This is just the metadata about a chunk that's coming on the binary channel
                const { sequence, totalChunks, size } = messageObj;

                // Store the expected chunk info for validation when we receive the binary data
                receiveState.expectedChunk = { sequence, totalChunks, size };

                console.debug(`[WebRTC] Expecting chunk ${sequence} of size ${size} bytes`);
            } else if (messageObj.type === 'chunk-confirm') {
                // Handle chunk confirmation from receiver
                const { sequence } = messageObj;

                // Update the last acknowledged sequence if this is the next expected one
                if (sequence === sendState.lastAckedSequence + 1) {
                    sendState.lastAckedSequence = sequence;

                    // Check for any consecutive acknowledged chunks
                    let nextSeq = sequence + 1;
                    while (nextSeq in sendState.unacknowledgedChunks &&
                           sendState.unacknowledgedChunks[nextSeq] === true) {
                        sendState.lastAckedSequence = nextSeq;
                        delete sendState.unacknowledgedChunks[nextSeq];
                        delete sendState.chunkTimestamps[nextSeq];
                        nextSeq++;
                    }
                } else if (sequence > sendState.lastAckedSequence) {
                    // Mark this chunk as acknowledged but don't update lastAckedSequence yet
                    sendState.unacknowledgedChunks[sequence] = true;
                }

                // Remove from unacknowledged chunks and timestamps
                if (sequence in sendState.unacknowledgedChunks) {
                    delete sendState.unacknowledgedChunks[sequence];
                    delete sendState.chunkTimestamps[sequence];
                }

                // Increase congestion window on successful ACK (TCP-like slow start/congestion avoidance)
                if (sendState.congestionWindow < sendState.windowSize) {
                    if (sendState.congestionWindow < 32) {
                        // Slow start - exponential growth
                        sendState.congestionWindow = Math.min(sendState.windowSize, sendState.congestionWindow + 1);
                    } else {
                        // Congestion avoidance - additive increase
                        sendState.congestionWindow = Math.min(
                            sendState.windowSize,
                            sendState.congestionWindow + (1 / sendState.congestionWindow)
                        );
                    }
                }

                // Reset consecutive timeouts counter on successful ACK
                sendState.consecutiveTimeouts = 0;

                // Continue sending if we have more chunks to send
                if (typeof window.trySendNextChunks === 'function') {
                    window.trySendNextChunks();
                }
            } else if (messageObj.type === 'request-chunks') {
                // Handle request for missing chunks
                const { sequences } = messageObj;

                if (Array.isArray(sequences) && sequences.length > 0) {
                    console.debug(`[WebRTC] Received request for chunks: ${sequences.join(', ')}`);

                    // Add requested chunks to retransmission queue
                    for (const sequence of sequences) {
                        if (!sendState.retransmissionQueue.includes(sequence)) {
                            sendState.retransmissionQueue.push(sequence);
                        }
                    }

                    // Sort retransmission queue
                    sendState.retransmissionQueue.sort((a, b) => a - b);

                    // Try to send the requested chunks
                    if (typeof window.trySendNextChunks === 'function') {
                        window.trySendNextChunks();
                    }
                }
            } else if (messageObj.type === 'capabilities') {
                // Handle capabilities message for chunk size negotiation
                if (messageObj.maxChunkSize) {
                    const peerMaxChunkSize = messageObj.maxChunkSize;
                    console.debug(`[WebRTC] Peer's maximum chunk size: ${peerMaxChunkSize}`);

                    // Import config to update the chunk size
                    import('/static/js/config.js').then(config => {
                        // Use the smaller of our max and peer's max
                        const negotiatedSize = Math.min(config.MAX_CHUNK_SIZE, peerMaxChunkSize);
                        config.CHUNK_SIZE = negotiatedSize;
                        console.debug(`[WebRTC] Negotiated chunk size: ${negotiatedSize}`);

                        // Send acknowledgment with the negotiated size
                        const dataChannel = getDataChannel();
                        if (dataChannel && dataChannel.readyState === 'open') {
                            dataChannel.send(JSON.stringify({
                                type: 'capabilities-ack',
                                negotiatedChunkSize: negotiatedSize
                            }));
                        }
                    });
                }
            } else if (messageObj.type === 'capabilities-ack') {
                // Handle capabilities acknowledgment
                if (messageObj.negotiatedChunkSize) {
                    const negotiatedSize = messageObj.negotiatedChunkSize;
                    console.debug(`[WebRTC] Peer acknowledged chunk size: ${negotiatedSize}`);

                    // Update our chunk size to the negotiated value
                    import('/static/js/config.js').then(config => {
                        config.CHUNK_SIZE = negotiatedSize;
                    });
                }
            } else if (messageObj.type === 'file-info-update') {
                // Handle MD5 hash update
                if (receiveState.inProgress && receiveState.fileInfo) {
                    if (messageObj.info.md5) {
                        receiveState.fileInfo.md5 = messageObj.info.md5;
                        console.debug(`[WebRTC] Updated file MD5 hash: ${messageObj.info.md5}`);
                    }
                }
            } else if (messageObj.type === 'file-complete') {
                // Only process file-complete if we're still in progress
                if (receiveState.inProgress) {
                    // Check for missing chunks before completing
                    const totalChunks = receiveState.buffer.length;
                    let missingCount = 0;

                    for (let i = 0; i < totalChunks; i++) {
                        if (!receiveState.receivedChunks.has(i)) {
                            missingCount++;
                            receiveState.missingChunks.add(i);
                        }
                    }

                    if (missingCount > 0) {
                        console.debug(`[WebRTC] File marked complete but missing ${missingCount} chunks. Requesting them...`);
                        requestMissingChunks();
                    } else {
                        receiveFile();
                    }
                } else {
                    console.debug('[WebRTC] Ignoring duplicate file-complete message');
                }
            }
        } catch (e) {
            console.error('[WebRTC] Failed to parse message:', e);
        }
    }
}

// Function to update the last received sequence number
function updateLastReceivedSequence() {
    let nextExpected = receiveState.lastReceivedSequence + 1;

    // Check if we have consecutive chunks
    while (receiveState.receivedChunks.has(nextExpected)) {
        receiveState.lastReceivedSequence = nextExpected;
        nextExpected++;
    }
}

// Function to check if we have all chunks
function checkIfComplete() {
    if (!receiveState.inProgress) return;

    const totalChunks = receiveState.buffer.length;

    // If we have all chunks, complete the transfer
    if (receiveState.receivedChunks.size === totalChunks) {
        console.debug('[WebRTC] All chunks received, completing transfer');
        receiveFile();
    }
}

// Function to check for missing chunks and request them
function checkForMissingChunks() {
    if (!receiveState.inProgress) {
        if (receiveState.retransmissionTimer) {
            clearInterval(receiveState.retransmissionTimer);
            receiveState.retransmissionTimer = null;
        }
        return;
    }

    const totalChunks = receiveState.buffer.length;
    const lastReceivedSequence = receiveState.lastReceivedSequence;

    // Calculate the window of chunks we expect to have received
    // Look ahead further to detect missing chunks earlier
    const lookAheadWindow = 50; // Look ahead up to 50 chunks
    const maxSequenceToCheck = Math.min(totalChunks - 1, lastReceivedSequence + lookAheadWindow);

    // First check for holes in the sequence (missing chunks)
    for (let i = 0; i <= lastReceivedSequence; i++) {
        if (!receiveState.receivedChunks.has(i) &&
            !receiveState.pendingRetransmissions.has(i)) {
            receiveState.missingChunks.add(i);
            console.debug(`[WebRTC] Detected missing chunk in sequence: ${i}`);
        }
    }

    // Then check for chunks we should have received based on the highest received chunk
    // This helps detect missing chunks even if they're not in sequence
    const highestReceivedChunk = Math.max(...receiveState.receivedChunks);
    if (highestReceivedChunk > lastReceivedSequence) {
        for (let i = lastReceivedSequence + 1; i < highestReceivedChunk; i++) {
            if (!receiveState.receivedChunks.has(i) &&
                !receiveState.pendingRetransmissions.has(i)) {
                receiveState.missingChunks.add(i);
                console.debug(`[WebRTC] Detected gap in received chunks: ${i}`);
            }
        }
    }

    // Also check for any chunks in the look-ahead window that we've missed
    // This is useful for detecting chunks that were sent but never arrived
    for (let i = Math.max(lastReceivedSequence + 1, highestReceivedChunk + 1);
         i <= maxSequenceToCheck; i++) {
        // Only consider it missing if we've received a higher sequence number
        // and it's been a while since we started receiving
        const transferTime = Date.now() - receiveState.startTime;
        if (transferTime > 2000 && // Only after 2 seconds of transfer
            highestReceivedChunk > i + 5 && // We've received chunks well beyond this one
            !receiveState.receivedChunks.has(i) &&
            !receiveState.pendingRetransmissions.has(i)) {
            receiveState.missingChunks.add(i);
            console.debug(`[WebRTC] Detected potentially skipped chunk: ${i}`);
        }
    }

    // Request missing chunks if we have any
    if (receiveState.missingChunks.size > 0) {
        requestMissingChunks();
    }

    // Check if we might be stalled (no progress for a while)
    const now = Date.now();
    if (receiveState.lastUpdate && (now - receiveState.lastUpdate > 5000)) {
        console.debug('[WebRTC] Transfer appears stalled, requesting any missing chunks');
        // Force a check of all chunks up to the highest we've seen
        const maxChunk = Math.max(lastReceivedSequence + 20, highestReceivedChunk + 1);
        for (let i = 0; i <= maxChunk && i < totalChunks; i++) {
            if (!receiveState.receivedChunks.has(i) &&
                !receiveState.pendingRetransmissions.has(i)) {
                receiveState.missingChunks.add(i);
            }
        }
        if (receiveState.missingChunks.size > 0) {
            requestMissingChunks();
        }
    }
}

// Function to request missing chunks
function requestMissingChunks() {
    if (receiveState.missingChunks.size === 0) return;

    const dataChannel = getDataChannel();
    if (!dataChannel || dataChannel.readyState !== 'open') return;

    // Convert Set to Array for JSON serialization
    const missingChunks = Array.from(receiveState.missingChunks);

    // Limit the number of chunks to request at once
    const chunksToRequest = missingChunks.slice(0, 50);

    console.debug(`[WebRTC] Requesting missing chunks: ${chunksToRequest.join(', ')}`);

    // Send request for missing chunks
    dataChannel.send(JSON.stringify({
        type: 'request-chunks',
        sequences: chunksToRequest
    }));

    // Mark these chunks as pending retransmission
    for (const sequence of chunksToRequest) {
        receiveState.pendingRetransmissions.add(sequence);
    }
}

// Calculate safe memory size for browsers without File System Access API
const maxSafeSize = 2 * 1024 * 1024 * 1024 - (100 * 1024 * 1024); // 2GB - 100MB overhead

// Send a file over the data channel
export async function sendFile(file) {
    const dataChannel = getDataChannel();
    if (!dataChannel || dataChannel.readyState !== 'open') return;

    if (sendState.inProgress) {
        ui.addSystemMessage("Cannot send file: Upload already in progress");
        return;
    }

    // Check file size limit for browsers without File System Access API
    if (!('showSaveFilePicker' in window) && file.size > maxSafeSize) {
        ui.addSystemMessage(
            `Error: File is too large (${formatBytes(file.size)}). ` +
            `Your browser supports files up to ${formatBytes(maxSafeSize)}. ` +
            `Please use Chrome or Edge for larger files.`
        );
        return;
    }

    // Initialize send state
    sendState.inProgress = true;
    sendState.currentFile = file;
    sendState.offset = 0;
    sendState.startTime = Date.now();
    sendState.lastUpdate = Date.now();
    sendState.lastOffset = 0;
    sendState.bytesPerSecond = 0;
    sendState.nextSequenceToSend = 0;
    sendState.lastAckedSequence = -1;
    sendState.unacknowledgedChunks = {};
    sendState.retransmissionQueue = [];

    // Clear any existing retransmission timer
    if (sendState.retransmissionTimer) {
        clearInterval(sendState.retransmissionTimer);
    }

    // Set up retransmission timer (check every 1 second for more responsive retransmissions)
    sendState.retransmissionTimer = setInterval(checkForRetransmissions, 1000);

    // Clear file selection immediately when starting transfer
    ui.resetFileInterface();

    // Calculate total chunks
    const fileSize = BigInt(file.size);
    const chunkSize = BigInt(CHUNK_SIZE);
    const totalChunks = Number((fileSize + chunkSize - BigInt(1)) / chunkSize);

    // Send file info first without waiting for MD5 hash
    dataChannel.send(JSON.stringify({
        type: 'file-info',
        info: {
            name: file.name,
            size: file.size,
            type: file.type,
            md5: '', // Will be updated later
            totalChunks: totalChunks
        }
    }));

    // Start the transfer immediately
    startSlidingWindowTransfer(file, totalChunks);

    // Calculate MD5 hash in the background
    calculateMD5(file).then(md5Hash => {
        console.debug(`[WebRTC] File MD5 hash: ${md5Hash}`);
        ui.addSystemMessage(`File checksum calculated: ${md5Hash}`);

        // Send updated file info with MD5 hash
        dataChannel.send(JSON.stringify({
            type: 'file-info-update',
            info: {
                md5: md5Hash
            }
        }));
    }).catch(error => {
        console.error('[WebRTC] Error calculating MD5:', error);
        ui.addSystemMessage(`Warning: Could not calculate file checksum. Integrity validation will be skipped.`);
    });

    ui.updateTransferProgress(0, `⬆ ${file.name}`, "send");
}

// Function to handle the sliding window transfer
function startSlidingWindowTransfer(file, totalChunks) {
    const dataChannel = getDataChannel();
    if (!dataChannel || dataChannel.readyState !== 'open') return;

    let activeReads = 0;
    const MAX_ACTIVE_READS = 5; // Increased concurrent file reads for better throughput

    // Reset congestion control parameters
    sendState.congestionWindow = sendState.windowSize;
    sendState.consecutiveTimeouts = 0;
    sendState.chunkTimestamps = {};

    // Function to send a specific chunk by sequence number
    const sendChunkBySequence = (sequence) => {
        if (sequence >= totalChunks) return;

        const offset = sequence * CHUNK_SIZE;
        const end = Math.min(offset + CHUNK_SIZE, file.size);
        const slice = file.slice(offset, end);

        activeReads++;
        const thisReader = new FileReader();

        thisReader.onload = function(event) {
            activeReads--;

            if (dataChannel.readyState !== 'open') return;

            const chunk = event.target.result;

            // Import the config to get the MAX_MESSAGE_SIZE
            import('/static/js/config.js').then(config => {
                // Start with an extremely conservative chunk size to avoid WebRTC message size issues
                // WebRTC has a message size limit that varies by implementation
                let dataSize = Math.min(chunk.byteLength, 8192); // 8KB is extremely safe for all implementations
                let chunkToSend = chunk.slice(0, dataSize);

                // Create a chunk info message for the control channel
                const chunkInfo = {
                    type: 'chunk-info',
                    sequence: sequence,
                    totalChunks: totalChunks,
                    size: dataSize
                };

                // Store binary data for potential retransmission
                sendState.unacknowledgedChunks[sequence] = {
                    info: chunkInfo,
                    data: chunkToSend
                };

                // Record timestamp when chunk was sent
                sendState.chunkTimestamps[sequence] = Date.now();

                // Send the chunk info on the control channel
                window.controlChannel.send(JSON.stringify(chunkInfo));

                // Add a longer delay to ensure the control message is processed first
                setTimeout(() => {
                    try {
                        // Send the binary data on the data channel
                        dataChannel.send(chunkToSend);

                        // Log success for debugging
                        console.debug(`[WebRTC] Sent binary chunk ${sequence} (${chunkToSend.byteLength} bytes)`);
                    } catch (error) {
                        console.error(`[WebRTC] Error sending binary chunk ${sequence}:`, error);

                        // Add to retransmission queue to try again
                        if (!sendState.retransmissionQueue.includes(sequence)) {
                            sendState.retransmissionQueue.push(sequence);
                        }
                    }
                }, 10);

                // Update progress based on next sequence to send
                sendState.offset = Math.min((sendState.nextSequenceToSend + 1) * CHUNK_SIZE, file.size);
                updateProgress();

                // Continue sending if window allows
                trySendNextChunks();
            });
        };

        thisReader.onerror = (error) => {
            activeReads--;
            console.error(`[WebRTC] Error reading chunk ${sequence}:`, error);

            // Add to retransmission queue to try again
            if (!sendState.retransmissionQueue.includes(sequence)) {
                sendState.retransmissionQueue.push(sequence);
            }

            trySendNextChunks();
        };

        thisReader.readAsArrayBuffer(slice);
    };

    // Function to try sending next chunks within the window
    const trySendNextChunks = () => {
        // Calculate effective window size (min of congestion window and configured window size)
        const effectiveWindowSize = Math.min(sendState.congestionWindow, sendState.windowSize);

        // First handle any retransmissions (prioritize them)
        while (sendState.retransmissionQueue.length > 0 &&
               activeReads < MAX_ACTIVE_READS &&
               dataChannel.bufferedAmount < CHUNK_SIZE * 16) {
            const sequence = sendState.retransmissionQueue.shift();

            // If we already have the chunk data, send it directly
            if (sendState.unacknowledgedChunks[sequence]) {
                const { info, data } = sendState.unacknowledgedChunks[sequence];

                // Send the chunk info on the control channel
                window.controlChannel.send(JSON.stringify(info));

                // Send the binary data on the data channel
                dataChannel.send(data);

                // Update timestamp for retransmitted chunk
                sendState.chunkTimestamps[sequence] = Date.now();
                console.debug(`[WebRTC] Retransmitted chunk ${sequence}`);
            } else {
                // Otherwise read it from the file
                sendChunkBySequence(sequence);
            }
        }

        // Then send new chunks within the window
        while (sendState.nextSequenceToSend < totalChunks &&
               sendState.nextSequenceToSend <= sendState.lastAckedSequence + effectiveWindowSize &&
               activeReads < MAX_ACTIVE_READS &&
               dataChannel.bufferedAmount < CHUNK_SIZE * 16) {

            sendChunkBySequence(sendState.nextSequenceToSend);
            sendState.nextSequenceToSend++;
        }

        // Check if we're done
        if (sendState.lastAckedSequence === totalChunks - 1) {
            finishTransfer();
        }
    };

    // Make trySendNextChunks globally available for chunk confirmations
    window.trySendNextChunks = trySendNextChunks;

    // Function to check for chunks that need retransmission
    window.checkForRetransmissions = () => {
        if (!sendState.inProgress) {
            if (sendState.retransmissionTimer) {
                clearInterval(sendState.retransmissionTimer);
                sendState.retransmissionTimer = null;
            }
            return;
        }

        // Check for unacknowledged chunks that might need retransmission
        const now = Date.now();
        const sequences = Object.keys(sendState.unacknowledgedChunks).map(Number);
        let timeoutsDetected = 0;

        if (sequences.length > 0) {
            console.debug(`[WebRTC] Unacknowledged chunks: ${sequences.length}`);

            // Add old unacknowledged chunks to retransmission queue
            for (const sequence of sequences) {
                if (sequence <= sendState.lastAckedSequence) {
                    // This was already ACKed, remove it
                    delete sendState.unacknowledgedChunks[sequence];
                    delete sendState.chunkTimestamps[sequence];
                } else {
                    const timestamp = sendState.chunkTimestamps[sequence];
                    // Check if chunk has timed out
                    if (timestamp && (now - timestamp) > sendState.retransmissionTimeout) {
                        if (!sendState.retransmissionQueue.includes(sequence)) {
                            // Add to retransmission queue
                            sendState.retransmissionQueue.push(sequence);
                            timeoutsDetected++;
                            console.debug(`[WebRTC] Queuing chunk ${sequence} for retransmission (timeout)`);
                        }
                    }
                }
            }

            // Implement congestion control
            if (timeoutsDetected > 0) {
                sendState.consecutiveTimeouts++;

                // Reduce window size on timeouts (TCP-like congestion avoidance)
                if (sendState.consecutiveTimeouts > 1) {
                    // Multiplicative decrease
                    sendState.congestionWindow = Math.max(8, Math.floor(sendState.congestionWindow * 0.7));
                    console.debug(`[WebRTC] Reducing congestion window to ${sendState.congestionWindow} due to timeouts`);
                }
            } else {
                sendState.consecutiveTimeouts = 0;

                // Additive increase if no timeouts
                if (sendState.congestionWindow < sendState.windowSize) {
                    sendState.congestionWindow = Math.min(
                        sendState.windowSize,
                        sendState.congestionWindow + 1
                    );
                }
            }

            // Sort retransmission queue by sequence number
            sendState.retransmissionQueue.sort((a, b) => a - b);

            // Try to send next chunks including retransmissions
            trySendNextChunks();
        }
    };

    // Start the transfer
    trySendNextChunks();
}

// Update transfer progress
function updateProgress() {
    if (!sendState.currentFile) return;

    // Use BigInt for precise calculation with large files
    const offset = BigInt(sendState.offset);
    const size = BigInt(sendState.currentFile.size);
    const percentage = Number((offset * BigInt(100)) / size);
    const now = Date.now();
    
    const timeDiff = now - sendState.lastUpdate;
    if (timeDiff >= PROGRESS_UPDATE_INTERVAL) {
        const instantRate = (sendState.offset - sendState.lastOffset) / timeDiff * 1000;
        sendState.bytesPerSecond = sendState.bytesPerSecond * (1 - BYTES_PER_SEC_SMOOTHING) + instantRate * BYTES_PER_SEC_SMOOTHING;
        sendState.lastUpdate = now;
        sendState.lastOffset = sendState.offset;
    }
    
    // Always show speed, even between updates
    ui.updateTransferProgress(percentage, `⬆ ${sendState.currentFile.name} - ${formatBytes(sendState.bytesPerSecond)}/s`, "send");
}

// Finish file transfer
function finishTransfer() {
    const dataChannel = getDataChannel();
    if (!dataChannel || dataChannel.readyState !== 'open') return;

    try {
        // Send file-complete message on the control channel
        window.controlChannel.send(JSON.stringify({
            type: 'file-complete'
        }));

        ui.addSystemMessage(`File sent: ${sendState.currentFile.name}`);
        showNotification('File Sent', `${sendState.currentFile.name} was sent successfully`);
        updateTitleWithSpinner(false);

        // Clean up resources
        if (sendState.retransmissionTimer) {
            clearInterval(sendState.retransmissionTimer);
            sendState.retransmissionTimer = null;
        }

        // Make trySendNextChunks globally available for chunk confirmations
        window.trySendNextChunks = null;

        setTimeout(() => {
            ui.hideTransferProgress("send");
            ui.resetFileInterface();
        }, 2000);

        // Reset state
        sendState.currentFile = null;
        sendState.offset = 0;
        sendState.inProgress = false;
        sendState.nextSequenceToSend = 0;
        sendState.lastAckedSequence = -1;
        sendState.unacknowledgedChunks = {};
        sendState.retransmissionQueue = [];
    } catch (error) {
        ui.addSystemMessage(`Error completing transfer: ${error}`);
        ui.hideTransferProgress("send");

        // Clean up resources even on error
        if (sendState.retransmissionTimer) {
            clearInterval(sendState.retransmissionTimer);
            sendState.retransmissionTimer = null;
        }

        window.trySendNextChunks = null;
        sendState.inProgress = false;
    }
}

// Helper function to validate file integrity
async function validateFileIntegrity(file) {
    if (!receiveState.fileInfo.md5) return;

    ui.updateConnectionStatus('Validating file integrity...');
    const receivedMD5 = await calculateMD5(file);
    console.debug(`[WebRTC] Received file MD5: ${receivedMD5}, Expected: ${receiveState.fileInfo.md5}`);
    
    if (receivedMD5 !== receiveState.fileInfo.md5) {
        ui.addSystemMessage(`⚠️ File integrity check failed! The file may be corrupted.`);
        showNotification('File Integrity Error', `${receiveState.fileInfo.name} failed checksum validation`);
    } else {
        ui.addSystemMessage(`✓ File integrity verified (MD5: ${receivedMD5})`);
    }
}

// Complete file reception and finalize
function createDownloadLink(blob, filename) {
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    setTimeout(() => URL.revokeObjectURL(url), 100);
}

async function receiveFile() {
    // Set inProgress to false immediately to prevent duplicate processing
    const wasInProgress = receiveState.inProgress;
    receiveState.inProgress = false;

    try {
        ui.updateConnectionStatus('Finalizing file...');

        // Clean up retransmission timer
        if (receiveState.retransmissionTimer) {
            clearInterval(receiveState.retransmissionTimer);
            receiveState.retransmissionTimer = null;
        }

        // Close the writable stream
        if (receiveState.fileWriter) {
            await receiveState.fileWriter.close();
            receiveState.fileWriter = null;
            const handle = receiveState.fileHandle;

            // Re-open the file for validation
            const file = await handle.getFile();
            await validateFileIntegrity(file);
        } else {
            // For browsers without File System Access API, create blob from chunks
            const chunks = [];

            // Verify all chunks are present
            const totalChunks = receiveState.buffer.length;
            for (let i = 0; i < totalChunks; i++) {
                const chunk = receiveState.buffer[i];
                if (!chunk) {
                    throw new Error(`Incomplete file transfer: missing chunk ${i}`);
                }
                chunks.push(chunk);
            }

            const blob = new Blob(chunks, { type: 'application/octet-stream' });
            await validateFileIntegrity(blob);
            createDownloadLink(blob, receiveState.fileInfo.name);
        }

        // Show success message
        ui.updateConnectionStatus('Connected to peer');
        ui.addSystemMessage(`✓ Transfer complete: ${receiveState.fileInfo.name}`);
        showNotification('File Ready', `${receiveState.fileInfo.name} has been saved`);
        updateTitleWithSpinner(false);

    } catch (error) {
        console.error('[WebRTC] Error saving file:', error);
        ui.addSystemMessage(`Error saving file: ${error.message}`);
        ui.showError(`Failed to save file: ${error.message}`);
    } finally {
        // Clean up
        setTimeout(() => {
            receiveState.buffer = [];
            receiveState.fileInfo = null;
            ui.hideTransferProgress("receive");
            receiveState.bytesPerSecond = 0;
            receiveState.startTime = 0;
            receiveState.lastUpdate = 0;
            receiveState.lastReceivedSequence = -1;
            receiveState.receivedChunks = new Set();
            receiveState.missingChunks = new Set();
            receiveState.pendingRetransmissions = new Set();
            // inProgress is already set to false at the beginning of the function
        }, 100);
    }
}

// Handle binary data channel messages
export async function handleDataMessage(event) {
    // Binary data should be an ArrayBuffer
    if (!(event.data instanceof ArrayBuffer)) {
        console.error('[WebRTC] Unexpected data type on binary channel:', typeof event.data);
        return;
    }

    // Check if we're expecting a chunk
    if (!receiveState.expectedChunk) {
        console.log('[WebRTC] Received binary data but no chunk info was received first');
        // This could be a race condition where the binary data arrived before the chunk info
        // We'll just ignore it as it will be retransmitted
        return;
    }

    // Get the expected chunk info and clear it immediately to prevent race conditions
    const expectedChunk = receiveState.expectedChunk;
    receiveState.expectedChunk = null;

    const { sequence, totalChunks, size } = expectedChunk;

    // Create a view of the data
    const dataView = new Uint8Array(event.data);

    // Validate the size
    if (dataView.byteLength !== size) {
        console.error(`[WebRTC] Chunk size mismatch. Expected: ${size}, Got: ${dataView.byteLength}`);
        return;
    }

    console.debug(`[WebRTC] Processing binary chunk ${sequence} (${dataView.byteLength} bytes)`);

    receiveState.fileInfo.currentChunk = { sequence, totalChunks, size };

    try {
        // Process the chunk
        await processChunk(dataView);

        // Mark this chunk as received
        receiveState.receivedChunks.add(sequence);

        // If this was a pending retransmission, remove it
        if (receiveState.pendingRetransmissions.has(sequence)) {
            receiveState.pendingRetransmissions.delete(sequence);
        }

        // If this was a missing chunk, remove it
        if (receiveState.missingChunks.has(sequence)) {
            receiveState.missingChunks.delete(sequence);
        }

        // Send chunk confirmation after successful processing with a small delay
        setTimeout(() => {
            if (window.controlChannel && window.controlChannel.readyState === 'open') {
                try {
                    window.controlChannel.send(JSON.stringify({
                        type: 'chunk-confirm',
                        sequence: sequence
                    }));
                    console.debug(`[WebRTC] Sent confirmation for chunk ${sequence}`);
                } catch (error) {
                    console.error(`[WebRTC] Error sending chunk confirmation:`, error);
                }
            }
        }, 5);

        // Check if we can update the last received sequence
        updateLastReceivedSequence();

        // Check if we have all chunks
        checkIfComplete();
    } catch (error) {
        console.error('[WebRTC] Error processing chunk:', error);
        ui.showError(`Failed to process chunk: ${error.message}`);
    }
}
