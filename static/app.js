// Constants
const CHUNK_SIZE = 65536; // 64KB chunks for better performance
const PROGRESS_UPDATE_INTERVAL = 200; // Update progress every 200ms
const WS_URL = `wss://${window.location.host}/ws`;
const BYTES_PER_SEC_SMOOTHING = 0.1; // EMA smoothing factor for transfer rate

// Transfer state tracking
let transferStartTime = 0;
let lastProgressUpdate = 0;
let bytesPerSecond = 0;


// WebRTC connection variables
let peerConnection;
let dataChannel;
let myToken = '';
let peerToken = '';
let websocket;
let selectedFile = null;
let receiveBuffer = [];
let receivedSize = 0;
let fileReceiveInfo = null;
let isConnecting = false;

// DOM Elements
const connectionPanel = document.getElementById('connection-panel');
const requestPanel = document.getElementById('request-panel');
const chatWindow = document.getElementById('chat-window');
const filePanel = document.getElementById('file-panel');
const messagesContainer = document.getElementById('messages');
const connectionStatus = document.getElementById('connection-status');

const myTokenInput = document.getElementById('my-token');
const peerTokenInput = document.getElementById('peer-token');
const requesterTokenSpan = document.getElementById('requester-token');
const messageInput = document.getElementById('message-input');

const copyTokenBtn = document.getElementById('copy-token');
const connectBtn = document.getElementById('connect-btn');
const acceptBtn = document.getElementById('accept-btn');
const rejectBtn = document.getElementById('reject-btn');
const sendBtn = document.getElementById('send-btn');
const sendFileBtn = document.getElementById('send-file-btn');
const fileInput = document.getElementById('file-input');
const fileInfo = document.getElementById('file-info');
const fileName = document.getElementById('file-name');
const fileSize = document.getElementById('file-size');
const transferProgress = document.getElementById('transfer-progress');
const transferStatus = document.getElementById('transfer-status');
const transferPercentage = document.getElementById('transfer-percentage');
const progressBar = document.getElementById('progress-bar');

// Initialize the application
function init() {
    setupWebSocket();
    setupEventListeners();
    setupNotifications();
    updateStatus('Connecting to server...');
}

// Set up browser notifications
function setupNotifications() {
    if ('Notification' in window) {
        if (Notification.permission !== 'granted' && Notification.permission !== 'denied') {
            Notification.requestPermission();
        }
    }
}

// Show browser notification
function showNotification(title, message) {
    if ('Notification' in window && Notification.permission === 'granted') {
        new Notification(title, { body: message });
    }
}

// Update page title with loading indicator
function updateTitleWithSpinner(isLoading) {
    const baseTitle = 'P2P File Transfer';
    document.title = isLoading ? `↻ ${baseTitle}` : baseTitle;
}

// Set up WebSocket connection
function setupWebSocket() {
    websocket = new WebSocket(WS_URL);

    websocket.onopen = () => {
        addSystemMessage('Connected to server');
        updateStatus('Connected to server');
    };

    websocket.onclose = () => {
        addSystemMessage('Disconnected from server');
        updateStatus('Disconnected from server');
    };

    websocket.onerror = (error) => {
        addSystemMessage('WebSocket error: ' + error);
        updateStatus('Connection error');
    };

    websocket.onmessage = (event) => {
        const message = JSON.parse(event.data);
        handleSignalingMessage(message);
    };
}

// Set up event listeners for UI elements
function setupEventListeners() {
    // Copy token button
    copyTokenBtn.addEventListener('click', () => {
        navigator.clipboard.writeText(myTokenInput.value)
            .then(() => {
                copyTokenBtn.textContent = 'Copied!';
                setTimeout(() => {
                    copyTokenBtn.textContent = 'Copy';
                }, 2000);
            })
            .catch(err => {
                console.error('Could not copy text: ', err);
            });
    });

    // Share URL button
    const shareUrlBtn = document.getElementById('share-url');
    shareUrlBtn.addEventListener('click', () => {
        const currentUrl = window.location.href.split('?')[0];
        const fullUrl = `${currentUrl}?token=${encodeURIComponent(myToken)}`;
        navigator.clipboard.writeText(fullUrl)
            .then(() => {
                shareUrlBtn.textContent = 'Copied!';
                setTimeout(() => {
                    shareUrlBtn.textContent = 'Share Link';
                }, 2000);
            })
            .catch(err => console.error('Copy failed:', err));
    });
    // Connect/Disconnect button
    connectBtn.addEventListener('click', () => {
        if (dataChannel && dataChannel.readyState === 'open') {
            // Disconnect if connected
            disconnectFromPeer();
        } else if (!isConnecting) {
            // Connect if not already connecting
            const token = peerTokenInput.value.trim();
            if (token && token !== myToken) {
                isConnecting = true;
                peerToken = token;
                connectBtn.textContent = 'Connecting...';
                connectBtn.classList.remove('bg-green-500', 'hover:bg-green-600');
                connectBtn.classList.add('bg-yellow-500', 'hover:bg-yellow-600');
                sendSignalingMessage({
                    type: 'connect',
                    peerToken: token
                });
                addSystemMessage(`Connection request sent to peer: ${token}`);
            } else {
                addSystemMessage('Please enter a valid peer token');
            }
        }
    });

    // Accept button for connection requests
    acceptBtn.addEventListener('click', () => {
        sendSignalingMessage({
            type: 'accept',
            peerToken: requesterTokenSpan.textContent
        });
        
        peerToken = requesterTokenSpan.textContent;
        initiatePeerConnection(false); // Not the initiator
        requestPanel.classList.add('hidden');
        updateStatus('Connecting to peer...');
    });

    // Reject button for connection requests
    rejectBtn.addEventListener('click', () => {
        sendSignalingMessage({
            type: 'reject',
            peerToken: requesterTokenSpan.textContent
        });
        requestPanel.classList.add('hidden');
    });

    // Send message button
    sendBtn.addEventListener('click', sendMessage);
    
    // Send message on Enter key press
    messageInput.addEventListener('keypress', (event) => {
        if (event.key === 'Enter') {
            sendMessage();
        }
    });

    // File input change
    fileInput.addEventListener('change', (event) => {
        selectedFile = event.target.files[0];
        if (selectedFile) {
            fileName.textContent = selectedFile.name;
            fileSize.textContent = formatBytes(selectedFile.size);
            fileInfo.classList.remove('hidden');
            sendFileBtn.disabled = false;
            sendFileBtn.classList.remove('opacity-50');
        }
    });

    // Send file button
    sendFileBtn.addEventListener('click', () => {
        if (selectedFile && dataChannel.readyState === 'open') {
            sendFile(selectedFile);
        }
    });
}

// Handle signaling messages from the server
function handleSignalingMessage(message) {
    switch (message.type) {
        case 'token':
            myToken = message.token;
            myTokenInput.value = myToken;
            addSystemMessage(`Your token: ${myToken}`);

            // Handle URL parameter for peer token
            const urlParams = new URLSearchParams(window.location.search);
            const peerTokenFromUrl = urlParams.get('token');
            if (peerTokenFromUrl && peerTokenFromUrl !== myToken) {
                peerTokenInput.value = peerTokenFromUrl;
                connectBtn.click();
            }
            break;
            
        case 'request':
            showConnectionRequest(message.token);
            break;
            
        case 'accepted':
            addSystemMessage(`Peer ${message.token} accepted your connection request`);
            peerToken = message.token;
            initiatePeerConnection(true); // Initiator
            break;
            
        case 'rejected':
            addSystemMessage(`Peer ${message.token} rejected your connection request`);
            break;
            
        case 'offer':
            handleOffer(message);
            break;
            
        case 'answer':
            handleAnswer(message);
            break;
            
        case 'ice':
            handleICECandidate(message);
            break;
            
        case 'error':
            addSystemMessage(`Error: ${message.sdp}`);
            break;
    }
}

// Display connection request
function showConnectionRequest(token) {
    requesterTokenSpan.textContent = token;
    requestPanel.classList.remove('hidden');
    showNotification('Connection Request', `Peer ${token} wants to connect with you`);
    updateTitleWithSpinner(true);
}

// Initialize WebRTC peer connection
function initiatePeerConnection(isInitiator) {
    const startTime = Date.now();
    console.debug('[WebRTC] Initiating peer connection...');

    // Create peer connection with optimized configuration for cross-browser compatibility
    // Use multiple STUN servers and public TURN servers for better NAT traversal
    const config = {
        iceServers: [
            // Primary STUN servers
            { urls: 'stun:stun.l.google.com:19302' },
            { urls: 'stun:stun1.l.google.com:19302' },
            // Public TURN servers for NAT traversal with TCP fallback
            {
                urls: [
                    'turn:openrelay.metered.ca:80',
                    'turn:openrelay.metered.ca:443',
                    'turn:openrelay.metered.ca:443?transport=tcp'
                ],
                username: 'openrelayproject',
                credential: 'openrelayproject'
            }
        ],
        iceTransportPolicy: 'all',
        bundlePolicy: 'max-bundle',
        rtcpMuxPolicy: 'require',
        iceCandidatePoolSize: 2,
        sdpSemantics: 'unified-plan',
        iceServersPolicy: 'all'  // Try all servers, not just the first working one
    };

    console.debug('[WebRTC] Creating peer connection with config:', config);
    peerConnection = new RTCPeerConnection(config);

    // Set common WebRTC options for reliability
    const dataChannelConfig = {
        ordered: true,        // Guarantee order of messages
        maxRetransmits: 30    // Increased retransmits for better reliability
    };

    // Log RTCPeerConnection state changes
    ['connectionState', 'iceConnectionState', 'iceGatheringState', 'signalingState'].forEach(prop => {
        const handler = () => {
            console.debug(`[WebRTC] ${prop}: ${peerConnection[prop]}`);
        };
        peerConnection[`on${prop}change`] = handler;
        handler(); // Log initial state
    });

    // Set up ICE candidate handling with extended gathering time
    let gatheringTimeoutId = null;
    const MAX_GATHERING_TIME = 8000; // 8 seconds max for gathering

    peerConnection.onicecandidate = (event) => {
        if (event.candidate) {
            // Clear any existing timeout since we're still receiving candidates
            if (gatheringTimeoutId) {
                clearTimeout(gatheringTimeoutId);
            }

            console.debug('[WebRTC] New ICE candidate:', {
                type: event.candidate.type,
                protocol: event.candidate.protocol,
                address: event.candidate.address,
                candidate: event.candidate.candidate
            });

            // Set new timeout for gathering completion
            gatheringTimeoutId = setTimeout(() => {
                if (peerConnection.iceGatheringState !== 'complete') {
                    console.debug('[WebRTC] Forcing ICE gathering completion after timeout');
                    const duration = Date.now() - startTime;
                    console.debug(`[WebRTC] Setup time: ${duration}ms`);
                }
            }, MAX_GATHERING_TIME);

            sendSignalingMessage({
                type: 'ice',
                peerToken: peerToken,
                ice: JSON.stringify(event.candidate)
            });
        } else {
            console.debug('[WebRTC] ICE gathering complete naturally');
            if (gatheringTimeoutId) {
                clearTimeout(gatheringTimeoutId);
            }
            const duration = Date.now() - startTime;
            console.debug(`[WebRTC] Setup time: ${duration}ms`);
        }
    };

    // Add ICE gathering state monitoring
    peerConnection.onicegatheringstatechange = () => {
        console.debug(`[WebRTC] ICE gathering state: ${peerConnection.iceGatheringState}`);
        if (peerConnection.iceGatheringState === 'complete') {
            if (gatheringTimeoutId) {
                clearTimeout(gatheringTimeoutId);
            }
        }
    };

    // Monitor ICE connection state
    peerConnection.oniceconnectionstatechange = () => {
        const state = peerConnection.iceConnectionState;
        const duration = Date.now() - startTime;
        console.debug(`[WebRTC] ICE state '${state}' after ${duration}ms`);
        addSystemMessage(`ICE Connection State: ${state}`);
        
        if (state === 'failed' || state === 'disconnected') {
            console.warn('[WebRTC] Connection problems detected, diagnostic info:', {
                iceConnectionState: peerConnection.iceConnectionState,
                connectionState: peerConnection.connectionState,
                signalingState: peerConnection.signalingState,
                localDescription: peerConnection.localDescription?.sdp,
                remoteDescription: peerConnection.remoteDescription?.sdp,
                setupTime: duration
            });
            
            // On failure, try to recover or cleanly disconnect
            addSystemMessage('Connection problems detected, attempting recovery...');
            
            // Only attempt recovery if we still have a valid connection
            if (peerConnection && !isConnectionDead()) {
                try {
                    console.debug('[WebRTC] Attempting connection recovery');
                    peerConnection.restartIce();
                    
                    // Set a timeout for the recovery attempt
                    setTimeout(() => {
                        if (peerConnection && peerConnection.iceConnectionState === 'failed') {
                            console.debug('[WebRTC] Recovery timeout, disconnecting');
                            disconnectFromPeer();
                        }
                    }, 5000);  // 5 second timeout for recovery
                } catch (error) {
                    console.error('[WebRTC] Recovery failed:', error);
                    disconnectFromPeer();
                }
            } else {
                console.debug('[WebRTC] Connection is dead, disconnecting');
                disconnectFromPeer();
            }
        }
    };

    // Monitor connection state
    peerConnection.onconnectionstatechange = () => {
        const state = peerConnection.connectionState;
        const duration = Date.now() - startTime;
        console.debug(`[WebRTC] Connection state '${state}' after ${duration}ms`);
        updateStatus(`WebRTC: ${state}`);
        
        if (state === 'failed') {
            console.error('[WebRTC] Connection failed, diagnostic info:', {
                iceConnectionState: peerConnection.iceConnectionState,
                connectionState: peerConnection.connectionState,
                signalingState: peerConnection.signalingState,
                localDescription: peerConnection.localDescription?.sdp,
                remoteDescription: peerConnection.remoteDescription?.sdp,
                setupTime: duration
            });
            addSystemMessage('Connection failed. You may need to reconnect.');
            disconnectFromPeer();
        }
    };

    // Log stats periodically for debugging
    const statsInterval = setInterval(async () => {
        if (peerConnection && peerConnection.connectionState === 'connected') {
            try {
                const stats = await peerConnection.getStats();
                let diagnostics = {};
                stats.forEach(stat => {
                    if (stat.type === 'candidate-pair' && stat.state === 'succeeded') {
                        diagnostics.activeCandidatePair = {
                            localType: stat.localCandidateType,
                            remoteType: stat.remoteCandidateType,
                            protocol: stat.protocol
                        };
                    }
                });
                console.debug('[WebRTC] Connection stats:', diagnostics);
            } catch (error) {
                console.warn('[WebRTC] Failed to get stats:', error);
            }
        }
    }, 10000);

    // Clean up stats interval on disconnect
    peerConnection.addEventListener('connectionstatechange', () => {
        if (peerConnection.connectionState === 'disconnected' || 
            peerConnection.connectionState === 'failed' || 
            peerConnection.connectionState === 'closed') {
            clearInterval(statsInterval);
        }
    });

    // Create data channel if initiator, or prepare to receive it
    if (isInitiator) {
        setupDataChannel(peerConnection.createDataChannel('p2pftp', dataChannelConfig));
        
        // Create and send offer
        peerConnection.createOffer()
            .then(offer => peerConnection.setLocalDescription(offer))
            .then(() => {
                sendSignalingMessage({
                    type: 'offer',
                    peerToken: peerToken,
                    sdp: JSON.stringify(peerConnection.localDescription)
                });
            })
            .catch(error => {
                addSystemMessage(`Error creating offer: ${error}`);
            });
    } else {
        peerConnection.ondatachannel = (event) => {
            setupDataChannel(event.channel);
        };
    }
}

// Set up data channel event handlers
function setupDataChannel(channel) {
    dataChannel = channel;
    
    dataChannel.onopen = () => {
        isConnecting = false;
        addSystemMessage('Peer connection established');
        updateStatus('Connected to peer');
        showChatAndFileInterface();
        connectBtn.textContent = 'Connected - Disconnect?';
        connectBtn.classList.remove('bg-yellow-500', 'hover:bg-yellow-600');
        connectBtn.classList.add('bg-red-500', 'hover:bg-red-600');
        peerTokenInput.disabled = true;
    };
    
    dataChannel.onclose = () => {
        addSystemMessage('Peer connection closed');
        updateStatus('Disconnected from peer');
        hideChatAndFileInterface();
        resetConnectionUI();
    };
    
    dataChannel.onerror = (error) => {
        addSystemMessage(`Data channel error: ${error}`);
    };
    
    dataChannel.onmessage = (event) => {
        const data = event.data;
        
        // If the data is a string, it's either a message or control data
        if (typeof data === 'string') {
            try {
                const messageObj = JSON.parse(data);
                
                if (messageObj.type === 'message') {
                    addPeerMessage(messageObj.content);
                } else if (messageObj.type === 'file-info') {
                    // Prepare to receive a file with ordered chunks and initialize transfer state
                    receiveBuffer = new Array(Math.ceil(messageObj.info.size / CHUNK_SIZE)); // Pre-allocate array
                    receivedSize = 0;
                    fileReceiveInfo = messageObj.info;
                    transferStartTime = Date.now();
                    lastProgressUpdate = transferStartTime;
                    bytesPerSecond = 0;
                    
                    addSystemMessage(`Receiving file: ${fileReceiveInfo.name} (${formatBytes(fileReceiveInfo.size)})`);
                    updateStatus(`Receiving file...`);
                    transferProgress.classList.remove('hidden');
                } else if (messageObj.type === 'chunk') {
                    // Store chunk at correct position
                    receiveBuffer[messageObj.sequence] = new Uint8Array(messageObj.data);
                    receivedSize += messageObj.data.byteLength;

                    const percentage = Math.min(Math.floor((receivedSize / fileReceiveInfo.size) * 100), 100);
                    progressBar.style.width = `${percentage}%`;
                    transferPercentage.textContent = `${percentage}%`;
                    transferStatus.textContent = `Receiving ${fileReceiveInfo.name} - Chunk ${messageObj.sequence + 1}/${messageObj.total}`;
                    
                    // Check if we received all chunks
                    if (receivedSize >= fileReceiveInfo.size) {
                        receiveFile();
                    }
                }
            } catch (e) {
                // Not JSON, treat as a regular message
                addPeerMessage(data);
            }
        } else {
            // Handle binary chunk
            if (!fileReceiveInfo) {
                console.error('[WebRTC] Received binary data without file info');
                return;
            }

            const chunk = new Uint8Array(event.data);
            receiveBuffer.push(chunk);
            receivedSize += chunk.byteLength;

            // Progress and transfer rate tracking
            const now = Date.now();
            if (now - lastProgressUpdate >= PROGRESS_UPDATE_INTERVAL) {
                const timeDiff = (now - transferStartTime) / 1000; // seconds
                const instantRate = chunk.byteLength / (now - lastProgressUpdate) * 1000; // bytes per second
                bytesPerSecond = bytesPerSecond * (1 - BYTES_PER_SEC_SMOOTHING) + instantRate * BYTES_PER_SEC_SMOOTHING;

                const percentage = Math.min(Math.floor((receivedSize / fileReceiveInfo.size) * 100), 100);
                progressBar.style.width = `${percentage}%`;
                transferPercentage.textContent = `${percentage}%`;
                transferStatus.textContent = `Receiving ${fileReceiveInfo.name} - ${formatBytes(bytesPerSecond)}/s`;

                console.debug(`[WebRTC] Transfer rate: ${formatBytes(bytesPerSecond)}/s`);
                lastProgressUpdate = now;
            }
            transferProgress.classList.remove('hidden');

            // Check if transfer is complete
            if (receivedSize >= fileReceiveInfo.size) {
                receiveFile();
            }
        }
    };
}

// Handle WebRTC offer
function handleOffer(message) {
    console.debug('[WebRTC] Received offer');
    const offer = JSON.parse(message.sdp);
    
    console.debug('[WebRTC] Offer SDP:', offer.sdp);
    
    peerConnection.setRemoteDescription(new RTCSessionDescription(offer))
        .then(() => {
            console.debug('[WebRTC] Remote description set, creating answer...');
            return peerConnection.createAnswer();
        })
        .then(answer => {
            console.debug('[WebRTC] Answer created:', answer.sdp);
            return peerConnection.setLocalDescription(answer);
        })
        .then(() => {
            console.debug('[WebRTC] Local description set, sending answer...');
            sendSignalingMessage({
                type: 'answer',
                peerToken: peerToken,
                sdp: JSON.stringify(peerConnection.localDescription)
            });
        })
        .catch(error => {
            console.error('[WebRTC] Error in offer/answer process:', error, {
                errorName: error.name,
                errorMessage: error.message,
                connectionState: peerConnection?.connectionState,
                iceConnectionState: peerConnection?.iceConnectionState,
                signalingState: peerConnection?.signalingState
            });
            addSystemMessage(`Error handling offer: ${error.name}: ${error.message}`);
        });
}

// Handle WebRTC answer
function handleAnswer(message) {
    console.debug('[WebRTC] Received answer');
    const answer = JSON.parse(message.sdp);
    
    console.debug('[WebRTC] Answer SDP:', answer.sdp);
    
    peerConnection.setRemoteDescription(new RTCSessionDescription(answer))
        .then(() => {
            console.debug('[WebRTC] Remote description set successfully');
        })
        .catch(error => {
            console.error('[WebRTC] Error handling answer:', error, {
                errorName: error.name,
                errorMessage: error.message,
                connectionState: peerConnection?.connectionState,
                iceConnectionState: peerConnection?.iceConnectionState,
                signalingState: peerConnection?.signalingState
            });
            addSystemMessage(`Error handling answer: ${error.name}: ${error.message}`);
        });
}

// Handle ICE candidate
function handleICECandidate(message) {
    const candidate = JSON.parse(message.ice);
    console.debug('[WebRTC] Received ICE candidate:', candidate.candidate);
    
    peerConnection.addIceCandidate(new RTCIceCandidate(candidate))
        .then(() => {
            console.debug('[WebRTC] Added ICE candidate successfully');
        })
        .catch(error => {
            console.error('[WebRTC] Error adding ICE candidate:', error, {
                candidate: candidate.candidate,
                errorName: error.name,
                errorMessage: error.message,
                connectionState: peerConnection?.connectionState,
                iceConnectionState: peerConnection?.iceConnectionState,
                signalingState: peerConnection?.signalingState
            });
            addSystemMessage(`Error adding ICE candidate: ${error.name}: ${error.message}`);
        });
}

// Send a signaling message through the WebSocket
function sendSignalingMessage(message) {
    if (websocket.readyState === WebSocket.OPEN) {
        websocket.send(JSON.stringify(message));
    } else {
        addSystemMessage('WebSocket is not connected');
    }
}

// Send a chat message
function sendMessage() {
    const message = messageInput.value.trim();
    if (message && dataChannel && dataChannel.readyState === 'open') {
        addMyMessage(message);
        
        dataChannel.send(JSON.stringify({
            type: 'message',
            content: message
        }));
        
        messageInput.value = '';
    }
}

// Send a file over the data channel
async function sendFile(file) {
    if (!dataChannel || dataChannel.readyState !== 'open') return;
    
    // Calculate MD5 hash before sending
    let md5Hash = '';
    try {
        addSystemMessage('Calculating file checksum...');
        md5Hash = await calculateMD5(file);
        console.debug(`[WebRTC] File MD5 hash: ${md5Hash}`);
        addSystemMessage(`File checksum calculated: ${md5Hash}`);
    } catch (error) {
        console.error('[WebRTC] Error calculating MD5:', error);
        addSystemMessage(`Warning: Could not calculate file checksum. Integrity validation will be skipped.`);
        // Continue without MD5 validation
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
    bytesPerSecond = 0;
    transferProgress.classList.remove('hidden');
    transferStatus.textContent = `Sending ${file.name}`;
    progressBar.style.width = '0%';
    transferPercentage.textContent = '0%';
    
    // Disable send button during transfer
    sendFileBtn.disabled = true;
    sendFileBtn.classList.add('opacity-50');
    
    // Read and send file in chunks with sequence numbers
    const reader = new FileReader();
    let offset = 0;
    let sequence = 0;
    const totalChunks = Math.ceil(file.size / CHUNK_SIZE);
    
    reader.onload = function(event) {
        if (dataChannel.readyState === 'open') {
            // Flow control: wait for buffer to clear
            if (dataChannel.bufferedAmount > CHUNK_SIZE * 8) {
                setTimeout(() => {
                    reader.onload(event);
                }, 100);
                return;
            }

            // Send binary chunk directly, no JSON wrapping
            dataChannel.send(event.target.result);
            sequence++;
            
            offset += event.target.result.byteLength;
            const percentage = Math.floor((offset / file.size) * 100);
            
            // Update UI
            progressBar.style.width = `${percentage}%`;
            transferPercentage.textContent = `${percentage}%`;
            transferStatus.textContent = `Sending ${file.name} - Chunk ${sequence}/${totalChunks}`;
            
    if (offset < file.size) {
        // More to send
        readSlice(offset);
    } else {
        // Done sending
        dataChannel.send(JSON.stringify({
            type: 'file-complete'
        }));
        
        addSystemMessage(`File sent: ${file.name}`);
        showNotification('File Sent', `${file.name} was sent successfully`);
        updateTitleWithSpinner(false);
        
        // Reset UI after a brief delay
        setTimeout(() => {
            transferProgress.classList.add('hidden');
            fileInfo.classList.add('hidden');
            fileInput.value = '';
            selectedFile = null;
            sendFileBtn.disabled = true;
            sendFileBtn.classList.add('opacity-50');
        }, 2000);
    }
        }
    };
    
    reader.onerror = (error) => {
        addSystemMessage(`Error reading file: ${error}`);
        transferProgress.classList.add('hidden');
    };
    
    function readSlice(offset) {
        const slice = file.slice(offset, offset + CHUNK_SIZE);
        reader.readAsArrayBuffer(slice);
    }
    
    // Start reading
    readSlice(0);
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
        updateStatus('Validating file integrity...');
        try {
            const receivedMD5 = await calculateMD5(received);
            console.debug(`[WebRTC] Received file MD5: ${receivedMD5}, Expected: ${fileReceiveInfo.md5}`);
            
            if (receivedMD5 !== fileReceiveInfo.md5) {
                addSystemMessage(`⚠️ File integrity check failed! The file may be corrupted.`);
                showNotification('File Integrity Error', `${fileReceiveInfo.name} failed checksum validation`);
                
                // Still provide the file, but with a warning
                const messageElement = document.createElement('div');
                messageElement.className = 'text-red-500 dark:text-red-400 text-sm py-1';
                messageElement.textContent = `Warning: File integrity check failed. The file may be corrupted or incomplete.`;
                messagesContainer.appendChild(messageElement);
            } else {
                addSystemMessage(`✓ File integrity verified (MD5: ${receivedMD5})`);
            }
        } catch (error) {
            console.error('[WebRTC] Error validating file MD5:', error);
            addSystemMessage(`Error validating file integrity: ${error.message}`);
        }
    }
    
    // Show notification and update title
    showNotification('File Received', `${fileReceiveInfo.name} is ready to download`);
    updateTitleWithSpinner(false);
    
    // Create message with download link
    const messageElement = document.createElement('div');
    messageElement.className = 'text-gray-500 dark:text-gray-400 text-sm py-1 italic';
    
    const downloadLink = document.createElement('a');
    downloadLink.href = URL.createObjectURL(received);
    downloadLink.download = fileReceiveInfo.name;
    downloadLink.className = 'text-blue-500 hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300 underline';
    downloadLink.textContent = `Download ${fileReceiveInfo.name} (${formatBytes(fileReceiveInfo.size)})`;
    
    // Clean up Blob URL after download starts
    downloadLink.addEventListener('click', () => {
        setTimeout(() => {
            URL.revokeObjectURL(downloadLink.href);
        }, 100);
    });
    
    messageElement.appendChild(document.createTextNode('File received: '));
    messageElement.appendChild(downloadLink);
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
    
    updateStatus('Connected to peer');
    
    // Reset file transfer state
    receiveBuffer = [];
    fileReceiveInfo = null;
    transferProgress.classList.add('hidden');
    bytesPerSecond = 0;
    transferStartTime = 0;
    lastProgressUpdate = 0;
}

// Add system message to chat
function addSystemMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'text-gray-500 dark:text-gray-400 text-sm py-1 italic';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

// Add peer message to chat
function addPeerMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'bg-gray-100 dark:bg-gray-700 p-2 rounded-lg max-w-[80%]';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

// Add my message to chat
function addMyMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'bg-blue-100 dark:bg-blue-900 p-2 rounded-lg max-w-[80%] ml-auto';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

// Update connection status
function updateStatus(status) {
    connectionStatus.textContent = status;
}

// Show chat and file interface once connected
function showChatAndFileInterface() {
    chatWindow.classList.remove('hidden');
    filePanel.classList.remove('hidden');
}

// Reset connection UI elements
function resetConnectionUI() {
    connectBtn.textContent = 'Connect';
    connectBtn.classList.remove('bg-yellow-500', 'hover:bg-yellow-600', 'bg-red-500', 'hover:bg-red-600');
    connectBtn.classList.add('bg-green-500', 'hover:bg-green-600');
    peerTokenInput.disabled = false;
    isConnecting = false;
    
    // Reset file transfer UI if active
    if (selectedFile || fileReceiveInfo) {
        transferProgress.classList.add('hidden');
        fileInfo.classList.add('hidden');
        fileInput.value = '';
        selectedFile = null;
        fileReceiveInfo = null;
        receiveBuffer = [];
        receivedSize = 0;
        sendFileBtn.disabled = true;
        sendFileBtn.classList.add('opacity-50');
    }
}

// Hide chat and file interface when disconnected
function hideChatAndFileInterface() {
    chatWindow.classList.add('hidden');
    filePanel.classList.add('hidden');
}

// Disconnect from peer
// Helper to check if connection is irrecoverable
function isConnectionDead() {
    return !peerConnection || 
           peerConnection.signalingState === 'closed' ||
           (peerConnection.iceConnectionState === 'failed' && 
            peerConnection.connectionState === 'failed');
}

function disconnectFromPeer() {
    console.debug('[WebRTC] Initiating disconnect');
    
    // Clean up data channel
    if (dataChannel) {
        try {
            dataChannel.close();
        } catch (error) {
            console.warn('[WebRTC] Error closing data channel:', error);
        }
        dataChannel = null;
    }

    // Clean up peer connection
    if (peerConnection) {
        try {
            peerConnection.close();
        } catch (error) {
            console.warn('[WebRTC] Error closing peer connection:', error);
        }
        peerConnection = null;
    }

    // Reset state
    peerToken = '';
    updateStatus('Disconnected');
    addSystemMessage('Disconnected from peer');
}

// Format bytes to human-readable format
function formatBytes(bytes, decimals = 2) {
    if (bytes === 0) return '0 Bytes';
    
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    
    return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

// Calculate MD5 hash of a file or blob
// Note: Web Crypto API doesn't support MD5 in some browsers as it's considered cryptographically weak
// This is a fallback implementation using SparkMD5 library with chunking for large files
async function calculateMD5(file) {
    return new Promise((resolve, reject) => {
        try {
            // Create a script element to load SparkMD5
            const script = document.createElement('script');
            script.src = 'https://cdnjs.cloudflare.com/ajax/libs/spark-md5/3.0.2/spark-md5.min.js';
            script.onload = () => {
                // For large files, we need to process in chunks to avoid memory issues
                const chunkSize = 2097152; // 2MB chunks for MD5 calculation
                const chunks = Math.ceil(file.size / chunkSize);
                let currentChunk = 0;
                
                const spark = new SparkMD5.ArrayBuffer();
                const fileReader = new FileReader();
                
                fileReader.onload = function(e) {
                    console.debug(`[WebRTC] MD5: Read chunk ${currentChunk + 1} of ${chunks}`);
                    spark.append(e.target.result); // Append chunk
                    currentChunk++;
                    
                    if (currentChunk < chunks) {
                        loadNext();
                    } else {
                        // Finalize hash calculation
                        const hashHex = spark.end();
                        console.debug(`[WebRTC] MD5 calculation complete: ${hashHex}`);
                        resolve(hashHex);
                    }
                };
                
                fileReader.onerror = function(e) {
                    reject(new Error("Error reading file for MD5 calculation"));
                };
                
                function loadNext() {
                    const start = currentChunk * chunkSize;
                    const end = Math.min(start + chunkSize, file.size);
                    const chunk = file.slice(start, end);
                    fileReader.readAsArrayBuffer(chunk);
                }
                
                // Start reading the first chunk
                loadNext();
            };
            
            script.onerror = () => {
                reject(new Error("Failed to load SparkMD5 library"));
            };
            
            document.head.appendChild(script);
        } catch (error) {
            reject(error);
        }
    });
}

// Initialize the application when the page loads
window.addEventListener('load', init);
