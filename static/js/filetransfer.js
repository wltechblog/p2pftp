import { CHUNK_SIZE, PROGRESS_UPDATE_INTERVAL, BYTES_PER_SEC_SMOOTHING, MAX_CHUNK_SIZE } from '/static/js/config.js';
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
    windowSize: 64,           // Number of chunks to send before waiting for acks
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
    // Send capabilities message with our maximum chunk size
    const dataChannel = getDataChannel();
    if (dataChannel && dataChannel.readyState === 'open') {
        dataChannel.send(JSON.stringify({
            type: 'capabilities',
            maxChunkSize: MAX_CHUNK_SIZE
        }));
    }

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
        receiveState.missingChunks.add(sequence);
        requestMissingChunks();
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
            receiveState.receivedChunks.add(sequence);
        } catch (error) {
            console.error('[WebRTC] Error writing chunk:', error);
            ui.showError(`Failed to write chunk: ${error.message}`);
            // Add failed chunk to missing chunks for retry
            receiveState.missingChunks.add(sequence);
            requestMissingChunks();
            return;
        }
    } else {
        // Fallback to buffer if no file handle yet
        receiveState.buffer[sequence] = chunk;
        receiveState.receivedSize += chunk.byteLength;
        receiveState.receivedChunks.add(sequence);
    }

    // If this was a pending retransmission, remove it
    if (receiveState.pendingRetransmissions.has(sequence)) {
        receiveState.pendingRetransmissions.delete(sequence);
    }

    // If this was a missing chunk, remove it
    if (receiveState.missingChunks.has(sequence)) {
        receiveState.missingChunks.delete(sequence);
    }

    // Update last received sequence and check for gaps
    updateLastReceivedSequence();
    checkForMissingChunks();

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

    // Send chunk acknowledgment
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

    // Check if we have all chunks
    checkIfComplete();
}

// Handle incoming control channel messages (JSON)
export async function handleControlMessage(event) {
    const data = event.data;

    if (typeof data === 'string') {
        try {
            const messageObj = JSON.parse(data);

            if (messageObj.type === 'file-info') {
                if (receiveState.inProgress) {
                    console.error('[WebRTC] Cannot receive file: Download already in progress');
                    return;
                }

                try {
                    let hasNativeFS = 'showSaveFilePicker' in window;
                    const fileSize = BigInt(messageObj.info.size);
                    const chunkSize = BigInt(CHUNK_SIZE); // Use negotiated chunk size
                    const numChunks = messageObj.info.totalChunks || Number((fileSize + chunkSize - BigInt(1)) / chunkSize);
                    receiveState.fileInfo = messageObj.info;
                    receiveState.fileInfo.totalChunks = numChunks;

                    if (hasNativeFS) {
                        const handle = await window.showSaveFilePicker({
                            suggestedName: messageObj.info.name,
                            types: [{
                                description: 'File',
                                accept: { '*/*': ['.'] }
                            }],
                        });

                        receiveState.fileHandle = handle;
                        receiveState.fileWriter = await handle.createWritable();
                        receiveState.buffer = new Array(numChunks).fill(false);
                    } else {
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
                    receiveState.totalChunks = numChunks;

                    // Clear any existing retransmission timer
                    if (receiveState.retransmissionTimer) {
                        clearInterval(receiveState.retransmissionTimer);
                    }

                    // Set up timer to check for missing chunks
                    receiveState.retransmissionTimer = setInterval(checkForMissingChunks, 1000);

                    ui.addSystemMessage(`Receiving file: ${receiveState.fileInfo.name} (${formatBytes(receiveState.fileInfo.size)})`);
                    ui.updateConnectionStatus(`Receiving file...`);
                    ui.updateTransferProgress(0, `⬇ ${receiveState.fileInfo.name}`, "receive");

                    // Send capabilities message with our maximum chunk size
                    window.controlChannel.send(JSON.stringify({
                        type: 'capabilities',
                        maxChunkSize: MAX_CHUNK_SIZE
                    }));

                } catch (error) {
                    console.error('[WebRTC] Error setting up file reception:', error);
                    ui.showError(`Failed to set up file reception: ${error.message}`);
                    return;
                }
            } else if (messageObj.type === 'chunk-info') {
                const { sequence, totalChunks, size } = messageObj;
                receiveState.fileInfo.currentChunk = { sequence, totalChunks, size };
                console.debug(`[WebRTC] Expecting chunk ${sequence} of size ${size} bytes`);
            } else if (messageObj.type === 'capabilities') {
                // Handle capabilities message
                if (messageObj.maxChunkSize) {
                    const peerMaxChunkSize = messageObj.maxChunkSize;
                    console.debug(`[WebRTC] Peer's maximum chunk size: ${peerMaxChunkSize}`);
                    
                    // Use the smaller of our max and peer's max
                    const negotiatedSize = Math.min(MAX_CHUNK_SIZE, peerMaxChunkSize);
                    CHUNK_SIZE = negotiatedSize;
                    console.debug(`[WebRTC] Negotiated chunk size: ${negotiatedSize}`);

                    // Send acknowledgment
                    window.controlChannel.send(JSON.stringify({
                        type: 'capabilities-ack',
                        negotiatedChunkSize: negotiatedSize
                    }));
                }
            } else if (messageObj.type === 'capabilities-ack') {
                if (messageObj.negotiatedChunkSize) {
                    CHUNK_SIZE = messageObj.negotiatedChunkSize;
                    console.debug(`[WebRTC] Using negotiated chunk size: ${CHUNK_SIZE}`);
                }
            } else if (messageObj.type === 'request-chunks') {
                // Handle request for missing chunks
                const { sequences } = messageObj;
                if (Array.isArray(sequences) && sequences.length > 0) {
                    console.debug(`[WebRTC] Received request for chunks: ${sequences.join(', ')}`);
                    for (const sequence of sequences) {
                        if (!sendState.retransmissionQueue.includes(sequence)) {
                            sendState.retransmissionQueue.push(sequence);
                        }
                    }
                    // Sort and process retransmission queue
                    sendState.retransmissionQueue.sort((a, b) => a - b);
                    window.trySendNextChunks();
                }
            } else if (messageObj.type === 'chunk-confirm') {
                handleChunkConfirmation(messageObj.sequence);
            }
        } catch (error) {
            console.error('[WebRTC] Failed to parse control message:', error);
        }
    }
}

// Handle chunk confirmation
function handleChunkConfirmation(sequence) {
    if (sequence === sendState.lastAckedSequence + 1) {
        sendState.lastAckedSequence = sequence;
        let nextSeq = sequence + 1;
        while (sendState.unacknowledgedChunks[nextSeq] === true) {
            sendState.lastAckedSequence = nextSeq;
            delete sendState.unacknowledgedChunks[nextSeq];
            delete sendState.chunkTimestamps[nextSeq];
            nextSeq++;
        }
    }

    // Update congestion window
    if (sendState.congestionWindow < sendState.windowSize) {
        if (sendState.congestionWindow < 32) {
            sendState.congestionWindow = Math.min(sendState.windowSize, sendState.congestionWindow + 1);
        } else {
            sendState.congestionWindow = Math.min(
                sendState.windowSize,
                sendState.congestionWindow + (1 / sendState.congestionWindow)
            );
        }
    }

    sendState.consecutiveTimeouts = 0;

    // Continue sending chunks if we're not done
    if (typeof window.trySendNextChunks === 'function') {
        window.trySendNextChunks();
    }
}

// Handle binary data channel messages
export async function handleDataMessage(event) {
    if (!(event.data instanceof ArrayBuffer)) {
        console.error('[WebRTC] Unexpected data type on binary channel:', typeof event.data);
        return;
    }

    // Parse frame header
    if (event.data.byteLength < 8) {
        console.error(`[WebRTC] Received binary data too small for frame header: ${event.data.byteLength} bytes`);
        return;
    }

    const headerView = new DataView(event.data, 0, 8);
    const sequence = headerView.getUint32(0, false); // big-endian
    const dataLength = headerView.getUint32(4, false);

    // Validate frame
    if (event.data.byteLength !== dataLength + 8) {
        console.error(`[WebRTC] Frame size mismatch. Header says ${dataLength} bytes data, got ${event.data.byteLength} bytes total`);
        // Request retransmission
        receiveState.missingChunks.add(sequence);
        requestMissingChunks();
        return;
    }

    // Extract data
    const data = new Uint8Array(dataLength);
    data.set(new Uint8Array(event.data, 8, dataLength));

    if (!receiveState.inProgress) {
        console.error('[WebRTC] Received chunk but no download is in progress');
        return;
    }

    receiveState.fileInfo.currentChunk = { 
        sequence, 
        totalChunks: receiveState.totalChunks, 
        size: dataLength 
    };

    // Process the chunk
    await processChunk(data);
}
