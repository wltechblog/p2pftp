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
    // No initialization needed now that we handle messages directly
}

// Process a chunk after converting to Uint8Array
function processChunk(chunk) {
    if (!receiveState.fileInfo || !receiveState.fileInfo.currentChunk) {
        console.error('[WebRTC] Missing chunk metadata');
        return;
    }

    const { sequence, total, size } = receiveState.fileInfo.currentChunk;
    console.debug(`[WebRTC] Processing chunk ${sequence + 1}/${total}, size: ${size}`);

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

    // Store chunk at correct position
    receiveState.buffer[sequence] = chunk;
    receiveState.receivedSize += chunk.byteLength;

    // Progress and transfer rate tracking
    const now = Date.now();
    if (now - receiveState.lastUpdate >= PROGRESS_UPDATE_INTERVAL) {
        const timeDiff = (now - receiveState.startTime) / 1000; // seconds
        const instantRate = chunk.byteLength / (now - receiveState.lastUpdate) * 1000; // bytes per second
        receiveState.bytesPerSecond = receiveState.bytesPerSecond * (1 - BYTES_PER_SEC_SMOOTHING) + instantRate * BYTES_PER_SEC_SMOOTHING;

        const percentage = Math.min(Math.floor((receiveState.receivedSize / receiveState.fileInfo.size) * 100), 100);
        ui.updateTransferProgress(percentage, `⬇ ${receiveState.fileInfo.name} - ${formatBytes(receiveState.bytesPerSecond)}/s`, "receive");

        receiveState.lastUpdate = now;
    } else {
        const percentage = Math.min(Math.floor((receiveState.receivedSize / receiveState.fileInfo.size) * 100), 100);
        ui.updateTransferProgress(percentage, `⬇ ${receiveState.fileInfo.name}`, "receive");
    }

    // Clear chunk info and check if complete
    delete receiveState.fileInfo.currentChunk;
    if (receiveState.receivedSize >= receiveState.fileInfo.size) {
        receiveFile();
    }
}

// Handle incoming data channel messages
export function handleDataChannelMessage(event) {
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
                
                // Prepare to receive a file
                receiveState.buffer = new Array(Math.ceil(messageObj.info.size / CHUNK_SIZE));
                receiveState.receivedSize = 0;
                receiveState.fileInfo = messageObj.info;
                receiveState.startTime = Date.now();
                receiveState.lastUpdate = Date.now();
                receiveState.bytesPerSecond = 0;
                receiveState.inProgress = true;
                
                ui.addSystemMessage(`Receiving file: ${receiveState.fileInfo.name} (${formatBytes(receiveState.fileInfo.size)})`);
                ui.updateConnectionStatus(`Receiving file...`);
                ui.updateTransferProgress(0, `⬇ ${receiveState.fileInfo.name}`, "receive");
            } else if (messageObj.type === 'chunk') {
                const { sequence, total, size, data } = messageObj;
                
                // Decode base64 data
                const binaryData = Uint8Array.from(atob(data), c => c.charCodeAt(0));
                
                if (binaryData.byteLength !== size) {
                    console.error(`[WebRTC] Chunk size mismatch. Expected: ${size}, Got: ${binaryData.byteLength}`);
                    return;
                }

                receiveState.fileInfo.currentChunk = { sequence, total, size };
                processChunk(binaryData);

                // Send chunk confirmation
                const dataChannel = getDataChannel();
                if (dataChannel && dataChannel.readyState === 'open') {
                    dataChannel.send(JSON.stringify({
                        type: 'chunk-confirm',
                        sequence: sequence
                    }));
                }
            } else if (messageObj.type === 'file-complete') {
                receiveFile();
            }
        } catch (e) {
            console.error('[WebRTC] Failed to parse message:', e);
        }
    }
}

// Send a file over the data channel
export async function sendFile(file) {
    const dataChannel = getDataChannel();
    if (!dataChannel || dataChannel.readyState !== 'open') return;
    
    if (sendState.inProgress) {
        ui.addSystemMessage("Cannot send file: Upload already in progress");
        return;
    }
    
    sendState.inProgress = true;
    sendState.currentFile = file;
    sendState.offset = 0;
    sendState.startTime = Date.now();
    sendState.lastUpdate = Date.now();
    sendState.lastOffset = 0;
    sendState.bytesPerSecond = 0;
    
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

        const chunkIndex = Math.floor(sendState.offset / CHUNK_SIZE);
        const totalChunks = Math.ceil(sendState.currentFile.size / CHUNK_SIZE);
        
        dataChannel.send(JSON.stringify({
            type: 'chunk',
            sequence: chunkIndex,
            total: totalChunks,
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
    const percentage = Math.floor((sendState.offset / sendState.currentFile.size) * 100);
    const now = Date.now();
    
    if (now - sendState.lastUpdate >= PROGRESS_UPDATE_INTERVAL) {
        const timeDiff = now - sendState.lastUpdate;
        const instantRate = (sendState.offset - sendState.lastOffset) / timeDiff * 1000;
        sendState.bytesPerSecond = sendState.bytesPerSecond * (1 - BYTES_PER_SEC_SMOOTHING) + instantRate * BYTES_PER_SEC_SMOOTHING;

        ui.updateTransferProgress(percentage, `⬆ ${sendState.currentFile.name} - ${formatBytes(sendState.bytesPerSecond)}/s`, "send");
        sendState.lastUpdate = now;
        sendState.lastOffset = sendState.offset;
    } else {
        ui.updateTransferProgress(percentage, `⬆ ${sendState.currentFile.name}`, "send");
    }
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

// Complete file reception and show download link
async function receiveFile() {
    const allData = new Uint8Array(receiveState.fileInfo.size);
    let offset = 0;
    
    for (const chunk of receiveState.buffer) {
        allData.set(chunk, offset);
        offset += chunk.length;
    }
    
    const received = new Blob([allData]);
    
    if (receiveState.fileInfo.md5) {
        ui.updateConnectionStatus('Validating file integrity...');
        try {
            const receivedMD5 = await calculateMD5(received);
            console.debug(`[WebRTC] Received file MD5: ${receivedMD5}, Expected: ${receiveState.fileInfo.md5}`);
            
            if (receivedMD5 !== receiveState.fileInfo.md5) {
                ui.addSystemMessage(`⚠️ File integrity check failed! The file may be corrupted.`);
                showNotification('File Integrity Error', `${receiveState.fileInfo.name} failed checksum validation`);
            } else {
                ui.addSystemMessage(`✓ File integrity verified (MD5: ${receivedMD5})`);
            }
        } catch (error) {
            console.error('[WebRTC] Error validating file MD5:', error);
            ui.addSystemMessage(`Error validating file integrity: ${error.message}`);
        }
    }
    
    const downloadUrl = URL.createObjectURL(received);
    ui.addFileDownloadMessage(receiveState.fileInfo, downloadUrl);
    ui.updateConnectionStatus('Connected to peer');
    showNotification('File Received', `${receiveState.fileInfo.name} is ready to download`);
    updateTitleWithSpinner(false);
    
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
