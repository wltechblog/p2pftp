import { CHUNK_SIZE, PROGRESS_UPDATE_INTERVAL, BYTES_PER_SEC_SMOOTHING } from '/static/js/config.js';
import { calculateMD5, formatBytes, showNotification, updateTitleWithSpinner } from '/static/js/utils.js';
import * as ui from '/static/js/ui.js';
import { getDataChannel } from '/static/js/webrtc.js';

// File transfer state
let receiveBuffer = [];
let receivedSize = 0;
let fileReceiveInfo = null;
let transferStartTime = 0;
let lastProgressUpdate = 0;
let lastOffset = 0;
let bytesPerSecond = 0;
let currentFile = null;
let currentOffset = 0;

// Initialize file transfer functionality
export function init() {
    // Listen for data channel messages
    window.addEventListener('datachannel-message', handleDataChannelMessage);
}

// Handle incoming data channel messages
function handleDataChannelMessage(event) {
    const data = event.detail.data;
    
    // If the data is a string, it's either a message or control data
    if (typeof data === 'string') {
        try {
            const messageObj = JSON.parse(data);
            
            if (messageObj.type === 'message') {
                ui.addPeerMessage(messageObj.content);
            } else if (messageObj.type === 'file-info') {
                // Prepare to receive a file
                receiveBuffer = new Array(Math.ceil(messageObj.info.size / CHUNK_SIZE)); // Pre-allocate array
                receivedSize = 0;
                fileReceiveInfo = messageObj.info;
                transferStartTime = Date.now();
                lastProgressUpdate = transferStartTime;
                bytesPerSecond = 0;
                
                ui.addSystemMessage(`Receiving file: ${fileReceiveInfo.name} (${formatBytes(fileReceiveInfo.size)})`);
                ui.updateConnectionStatus(`Receiving file...`);
                ui.updateTransferProgress(0, `Receiving ${fileReceiveInfo.name}`);
            } else if (messageObj.type === 'chunk') {
                // Store chunk at correct position
                receiveBuffer[messageObj.sequence] = new Uint8Array(messageObj.data);
                receivedSize += messageObj.data.byteLength;

                const percentage = Math.min(Math.floor((receivedSize / fileReceiveInfo.size) * 100), 100);
                ui.updateTransferProgress(percentage, `Receiving ${fileReceiveInfo.name} - Chunk ${messageObj.sequence + 1}/${messageObj.total}`);
                
                // Check if we received all chunks
                if (receivedSize >= fileReceiveInfo.size) {
                    receiveFile();
                }
            }
        } catch (e) {
            // Not JSON, treat as a regular message
            ui.addPeerMessage(data);
        }
    } else {
        // Handle binary chunk
        if (!fileReceiveInfo) {
            console.error('[WebRTC] Received binary data without file info');
            return;
        }

        const chunk = new Uint8Array(event.detail.data);
        receiveBuffer.push(chunk);
        receivedSize += chunk.byteLength;

        // Progress and transfer rate tracking
        const now = Date.now();
        if (now - lastProgressUpdate >= PROGRESS_UPDATE_INTERVAL) {
            const timeDiff = (now - transferStartTime) / 1000; // seconds
            const instantRate = chunk.byteLength / (now - lastProgressUpdate) * 1000; // bytes per second
            bytesPerSecond = bytesPerSecond * (1 - BYTES_PER_SEC_SMOOTHING) + instantRate * BYTES_PER_SEC_SMOOTHING;

            const percentage = Math.min(Math.floor((receivedSize / fileReceiveInfo.size) * 100), 100);
            ui.updateTransferProgress(percentage, `Receiving ${fileReceiveInfo.name} - ${formatBytes(bytesPerSecond)}/s`);

            console.debug(`[WebRTC] Transfer rate: ${formatBytes(bytesPerSecond)}/s`);
            lastProgressUpdate = now;
        }

        // Check if transfer is complete
        if (receivedSize >= fileReceiveInfo.size) {
            receiveFile();
        }
    }
}

// Send a file over the data channel
export async function sendFile(file) {
    const dataChannel = getDataChannel();
    if (!dataChannel || dataChannel.readyState !== 'open') return;
    
    currentFile = file;
    currentOffset = 0;
    
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
    
    // Initialize transfer state and show progress UI
    transferStartTime = Date.now();
    lastProgressUpdate = transferStartTime;
    lastOffset = 0;
    bytesPerSecond = 0;
    ui.updateTransferProgress(0, `Sending ${file.name}`);
    
    // Read and send file in chunks
    const reader = new FileReader();
    const totalChunks = Math.ceil(file.size / CHUNK_SIZE);
    
    reader.onload = function(event) {
        if (dataChannel.readyState === 'open') {
            // Send binary chunk with flow control
            const chunk = event.target.result;
            const maxBufferSize = CHUNK_SIZE * 8;

            // If buffer is getting full, wait for it to clear
            if (dataChannel.bufferedAmount > maxBufferSize) {
                const waitAndSend = () => {
                    if (dataChannel.bufferedAmount > maxBufferSize) {
                        setTimeout(waitAndSend, 100);
                        return;
                    }
                    try {
                        dataChannel.send(chunk);
                        const bytesSent = chunk.byteLength;
                        currentOffset += bytesSent;
                        updateProgress();
                        
                        if (currentOffset < file.size) {
                            readSlice(currentOffset);
                        } else {
                            finishTransfer();
                        }
                    } catch (error) {
                        ui.addSystemMessage(`Error sending chunk: ${error}`);
                        ui.hideTransferProgress();
                    }
                };
                setTimeout(waitAndSend, 100);
                return;
            }

            // Buffer is clear enough, send immediately
            try {
                dataChannel.send(chunk);
            } catch (error) {
                ui.addSystemMessage(`Error sending chunk: ${error}`);
                ui.hideTransferProgress();
                return;
            }
            
            const bytesSent = chunk.byteLength;
            currentOffset += bytesSent;
            updateProgress();
            
            if (currentOffset < file.size) {
                readSlice(currentOffset);
            } else {
                finishTransfer();
            }
        }
    };
    
    reader.onerror = (error) => {
        ui.addSystemMessage(`Error reading file: ${error}`);
        ui.hideTransferProgress();
    };
    
    function readSlice(offset) {
        const slice = file.slice(offset, offset + CHUNK_SIZE);
        reader.readAsArrayBuffer(slice);
    }
    
    // Start reading
    readSlice(0);
}

// Update transfer progress
function updateProgress() {
    if (!currentFile) return;
    const percentage = Math.floor((currentOffset / currentFile.size) * 100);
    const now = Date.now();
    
    if (now - lastProgressUpdate >= PROGRESS_UPDATE_INTERVAL) {
        const timeDiff = now - lastProgressUpdate;
        const instantRate = (currentOffset - lastOffset) / timeDiff * 1000; // bytes per second
        bytesPerSecond = bytesPerSecond * (1 - BYTES_PER_SEC_SMOOTHING) + instantRate * BYTES_PER_SEC_SMOOTHING;

        ui.updateTransferProgress(percentage, `Sending ${currentFile.name} - ${formatBytes(bytesPerSecond)}/s`);
        console.debug(`[WebRTC] Upload rate: ${formatBytes(bytesPerSecond)}/s`);
        
        lastProgressUpdate = now;
        lastOffset = currentOffset;
    } else {
        ui.updateTransferProgress(percentage, `Sending ${currentFile.name}`);
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
        
        ui.addSystemMessage(`File sent: ${currentFile.name}`);
        showNotification('File Sent', `${currentFile.name} was sent successfully`);
        updateTitleWithSpinner(false);
        
        // Reset UI after a brief delay
        setTimeout(() => {
            ui.hideTransferProgress();
            ui.resetFileInterface();
        }, 2000);
        
        // Reset file state
        currentFile = null;
        currentOffset = 0;
    } catch (error) {
        ui.addSystemMessage(`Error completing transfer: ${error}`);
        ui.hideTransferProgress();
    }
}

// Complete file reception and show download link
async function receiveFile() {
    // Pre-allocate array for the complete file
    const allData = new Uint8Array(fileReceiveInfo.size);
    let offset = 0;
    
    // Copy chunks in order
    for (const chunk of receiveBuffer) {
        allData.set(chunk, offset);
        offset += chunk.length;
    }
    
    // Create blob from the complete array
    const received = new Blob([allData]);
    
    // Validate MD5 checksum if provided
    if (fileReceiveInfo.md5) {
        ui.updateConnectionStatus('Validating file integrity...');
        try {
            const receivedMD5 = await calculateMD5(received);
            console.debug(`[WebRTC] Received file MD5: ${receivedMD5}, Expected: ${fileReceiveInfo.md5}`);
            
            if (receivedMD5 !== fileReceiveInfo.md5) {
                ui.addSystemMessage(`⚠️ File integrity check failed! The file may be corrupted.`);
                showNotification('File Integrity Error', `${fileReceiveInfo.name} failed checksum validation`);
            } else {
                ui.addSystemMessage(`✓ File integrity verified (MD5: ${receivedMD5})`);
            }
        } catch (error) {
            console.error('[WebRTC] Error validating file MD5:', error);
            ui.addSystemMessage(`Error validating file integrity: ${error.message}`);
        }
    }
    
    // Show notification and update title
    showNotification('File Received', `${fileReceiveInfo.name} is ready to download`);
    updateTitleWithSpinner(false);
    
    // Create download link
    const downloadUrl = URL.createObjectURL(received);
    ui.addFileDownloadMessage(fileReceiveInfo, downloadUrl);
    ui.updateConnectionStatus('Connected to peer');
    
    // Reset file transfer state
    receiveBuffer = [];
    fileReceiveInfo = null;
    ui.hideTransferProgress();
    bytesPerSecond = 0;
    transferStartTime = 0;
    lastProgressUpdate = 0;
}
