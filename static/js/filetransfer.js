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
    inProgress: false
};

const sendState = {
    currentFile: null,
    offset: 0,
    startTime: 0,
    lastUpdate: 0,
    lastOffset: 0,
    bytesPerSecond: 0,
    inProgress: false
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

    // Clear chunk info and check if complete
    delete receiveState.fileInfo.currentChunk;
    if (receiveState.receivedSize >= receiveState.fileInfo.size) {
        receiveFile();
    }
}

// Handle incoming data channel messages
export async function handleDataChannelMessage(event) {
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
                    const numChunks = Number((fileSize + chunkSize - BigInt(1)) / chunkSize);
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
                    
                    ui.addSystemMessage(`Receiving file: ${receiveState.fileInfo.name} (${formatBytes(receiveState.fileInfo.size)})`);
                    ui.updateConnectionStatus(`Receiving file...`);
                    ui.updateTransferProgress(0, `⬇ ${receiveState.fileInfo.name}`, "receive");
                } catch (error) {
                    console.error('[WebRTC] Error setting up file reception:', error);
                    ui.showError(`Failed to set up file reception: ${error.message}`);
                    return;
                }
            } else if (messageObj.type === 'chunk') {
                const { sequence, totalChunks, size, data } = messageObj;
                
                // Decode base64 data
                const binaryData = Uint8Array.from(atob(data), c => c.charCodeAt(0));
                
                if (binaryData.byteLength !== size) {
                    console.error(`[WebRTC] Chunk size mismatch. Expected: ${size}, Got: ${binaryData.byteLength}`);
                    return;
                }

                receiveState.fileInfo.currentChunk = { sequence, totalChunks, size };
                try {
                    await processChunk(binaryData);
                    // Send chunk confirmation after successful processing
                    const dataChannel = getDataChannel();
                    if (dataChannel && dataChannel.readyState === 'open') {
                        dataChannel.send(JSON.stringify({
                            type: 'chunk-confirm',
                            sequence: sequence
                        }));
                    }
                } catch (error) {
                    console.error('[WebRTC] Error processing chunk:', error);
                    ui.showError(`Failed to process chunk: ${error.message}`);
                    return;
                }
            } else if (messageObj.type === 'file-complete') {
                receiveFile();
            }
        } catch (e) {
            console.error('[WebRTC] Failed to parse message:', e);
        }
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
    
    sendState.inProgress = true;
    sendState.currentFile = file;
    sendState.offset = 0;
    sendState.startTime = Date.now();
    sendState.lastUpdate = Date.now();
    sendState.lastOffset = 0;
    sendState.bytesPerSecond = 0;

    // Clear file selection immediately when starting transfer
    ui.resetFileInterface();
    
    // Calculate MD5 hash before sending
    let md5Hash = '';
    try {
        ui.addSystemMessage('Calculating file checksum...');
        md5Hash = await calculateMD5(file);
        console.debug(`[WebRTC] File MD5 hash: ${md5Hash}`);
        ui.addSystemMessage(`File checksum calculated: ${md5Hash}`);
    } catch (error) {
        console.error('[WebRTC] Error calculating MD5:', error);
        ui.addSystemMessage(`Warning: Could not calculate file checksum. Integrity validation will be skipped.`);
    }
    
    // Send file info first with MD5 hash
    dataChannel.send(JSON.stringify({
        type: 'file-info',
        info: {
            name: file.name,
            size: file.size,
            type: file.type,
            md5: md5Hash
        }
    }));
    
    ui.updateTransferProgress(0, `⬆ ${file.name}`, "send");
    
    // Read and send file in chunks
    const reader = new FileReader();
    
    const sendChunk = (chunk) => {
        if (!dataChannel || dataChannel.readyState !== 'open') return;

        // Use BigInt for large file handling
        const offset = BigInt(sendState.offset);
        const chunkSize = BigInt(CHUNK_SIZE);
        const fileSize = BigInt(sendState.currentFile.size);
        const chunkIndex = Number(offset / chunkSize);
        const totalChunks = Number((fileSize + chunkSize - BigInt(1)) / chunkSize);
        
        dataChannel.send(JSON.stringify({
            type: 'chunk',
            sequence: chunkIndex,
            totalChunks: totalChunks,
            size: chunk.byteLength,
            data: btoa(String.fromCharCode.apply(null, new Uint8Array(chunk)))
        }));

        sendState.offset += chunk.byteLength;
        updateProgress();
        
        if (sendState.offset < sendState.currentFile.size) {
            readNextSlice();
        } else {
            finishTransfer();
        }
    };

    const readNextSlice = () => {
        const slice = file.slice(sendState.offset, sendState.offset + CHUNK_SIZE);
        reader.readAsArrayBuffer(slice);
    };
    
    reader.onload = function(event) {
        if (dataChannel.readyState === 'open') {
            const chunk = event.target.result;
            const maxBufferSize = CHUNK_SIZE * 8;

            if (dataChannel.bufferedAmount > maxBufferSize) {
                const waitAndSend = () => {
                    if (dataChannel.bufferedAmount > maxBufferSize) {
                        setTimeout(waitAndSend, 100);
                        return;
                    }
                    sendChunk(chunk);
                };
                setTimeout(waitAndSend, 100);
                return;
            }

            sendChunk(chunk);
        }
    };
    
    reader.onerror = (error) => {
        ui.addSystemMessage(`Error reading file: ${error}`);
        ui.hideTransferProgress("send");
        sendState.inProgress = false;
    };
    
    readNextSlice();
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
        dataChannel.send(JSON.stringify({
            type: 'file-complete'
        }));
        
        ui.addSystemMessage(`File sent: ${sendState.currentFile.name}`);
        showNotification('File Sent', `${sendState.currentFile.name} was sent successfully`);
        updateTitleWithSpinner(false);
        
        setTimeout(() => {
            ui.hideTransferProgress("send");
            ui.resetFileInterface();
        }, 2000);
        
        sendState.currentFile = null;
        sendState.offset = 0;
        sendState.inProgress = false;
    } catch (error) {
        ui.addSystemMessage(`Error completing transfer: ${error}`);
        ui.hideTransferProgress("send");
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
    try {
        ui.updateConnectionStatus('Finalizing file...');

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
            for (const chunk of receiveState.buffer) {
                if (!chunk) {
                    throw new Error("Incomplete file transfer");
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
            receiveState.inProgress = false;
        }, 100);
    }
}
