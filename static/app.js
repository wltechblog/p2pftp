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

// Constants
const CHUNK_SIZE = 16384; // 16KB chunks
const WS_URL = `ws://${window.location.host}/ws`;

// Initialize the application
function init() {
    setupWebSocket();
    setupEventListeners();
    updateStatus('Connecting to server...');
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

    // Connect button
    connectBtn.addEventListener('click', () => {
        const token = peerTokenInput.value.trim();
        if (token && token !== myToken) {
            peerToken = token;
            sendSignalingMessage({
                type: 'connect',
                peerToken: token
            });
            addSystemMessage(`Connection request sent to peer: ${token}`);
        } else {
            addSystemMessage('Please enter a valid peer token');
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
}

// Initialize WebRTC peer connection
function initiatePeerConnection(isInitiator) {
    // Create peer connection
    peerConnection = new RTCPeerConnection({
        iceServers: [
            { urls: 'stun:stun.l.google.com:19302' },
            { urls: 'stun:stun1.l.google.com:19302' }
        ]
    });

    // Set up ICE candidate handling
    peerConnection.onicecandidate = (event) => {
        if (event.candidate) {
            sendSignalingMessage({
                type: 'ice',
                peerToken: peerToken,
                ice: JSON.stringify(event.candidate)
            });
        }
    };

    // Monitor connection state
    peerConnection.onconnectionstatechange = () => {
        updateStatus(`WebRTC: ${peerConnection.connectionState}`);
    };

    // Create data channel if initiator, or prepare to receive it
    if (isInitiator) {
        setupDataChannel(peerConnection.createDataChannel('p2pftp'));
        
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
        addSystemMessage('Peer connection established');
        updateStatus('Connected to peer');
        showChatAndFileInterface();
    };
    
    dataChannel.onclose = () => {
        addSystemMessage('Peer connection closed');
        updateStatus('Disconnected from peer');
        hideChatAndFileInterface();
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
                    // Prepare to receive a file
                    receiveBuffer = [];
                    receivedSize = 0;
                    fileReceiveInfo = messageObj.info;
                    
                    addSystemMessage(`Receiving file: ${fileReceiveInfo.name} (${formatBytes(fileReceiveInfo.size)})`);
                    updateStatus(`Receiving file...`);
                } else if (messageObj.type === 'file-complete') {
                    // File transfer is complete
                    receiveFile();
                }
            } catch (e) {
                // Not JSON, treat as a regular message
                addPeerMessage(data);
            }
        } else {
            // Binary data - file chunk
            receiveBuffer.push(data);
            receivedSize += data.byteLength;
            
            // Update progress if we have file info
            if (fileReceiveInfo) {
                const percentage = Math.floor((receivedSize / fileReceiveInfo.size) * 100);
                progressBar.style.width = `${percentage}%`;
                transferPercentage.textContent = `${percentage}%`;
                transferStatus.textContent = `Receiving ${fileReceiveInfo.name}`;
                transferProgress.classList.remove('hidden');
            }
        }
    };
}

// Handle WebRTC offer
function handleOffer(message) {
    const offer = JSON.parse(message.sdp);
    peerConnection.setRemoteDescription(new RTCSessionDescription(offer))
        .then(() => peerConnection.createAnswer())
        .then(answer => peerConnection.setLocalDescription(answer))
        .then(() => {
            sendSignalingMessage({
                type: 'answer',
                peerToken: peerToken,
                sdp: JSON.stringify(peerConnection.localDescription)
            });
        })
        .catch(error => {
            addSystemMessage(`Error handling offer: ${error}`);
        });
}

// Handle WebRTC answer
function handleAnswer(message) {
    const answer = JSON.parse(message.sdp);
    peerConnection.setRemoteDescription(new RTCSessionDescription(answer))
        .catch(error => {
            addSystemMessage(`Error handling answer: ${error}`);
        });
}

// Handle ICE candidate
function handleICECandidate(message) {
    const candidate = JSON.parse(message.ice);
    peerConnection.addIceCandidate(new RTCIceCandidate(candidate))
        .catch(error => {
            addSystemMessage(`Error adding ICE candidate: ${error}`);
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
function sendFile(file) {
    if (!dataChannel || dataChannel.readyState !== 'open') return;
    
    // Send file info first
    dataChannel.send(JSON.stringify({
        type: 'file-info',
        info: {
            name: file.name,
            size: file.size,
            type: file.type
        }
    }));
    
    // Show progress UI
    transferProgress.classList.remove('hidden');
    transferStatus.textContent = `Sending ${file.name}`;
    progressBar.style.width = '0%';
    transferPercentage.textContent = '0%';
    
    // Disable send button during transfer
    sendFileBtn.disabled = true;
    sendFileBtn.classList.add('opacity-50');
    
    // Read and send file in chunks
    const reader = new FileReader();
    let offset = 0;
    
    reader.onload = function(event) {
        if (dataChannel.readyState === 'open') {
            dataChannel.send(event.target.result);
            
            offset += event.target.result.byteLength;
            const percentage = Math.floor((offset / file.size) * 100);
            
            progressBar.style.width = `${percentage}%`;
            transferPercentage.textContent = `${percentage}%`;
            
            if (offset < file.size) {
                // More to send
                readSlice(offset);
            } else {
                // Done sending
                dataChannel.send(JSON.stringify({
                    type: 'file-complete'
                }));
                
                addSystemMessage(`File sent: ${file.name}`);
                
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

// Complete file reception and trigger download
function receiveFile() {
    // Combine received buffer into a single Blob
    const received = new Blob(receiveBuffer);
    receiveBuffer = [];
    
    // Create download link
    const downloadLink = document.createElement('a');
    downloadLink.href = URL.createObjectURL(received);
    downloadLink.download = fileReceiveInfo.name;
    downloadLink.style.display = 'none';
    document.body.appendChild(downloadLink);
    
    // Trigger download and clean up
    downloadLink.click();
    setTimeout(() => {
        document.body.removeChild(downloadLink);
        URL.revokeObjectURL(downloadLink.href);
    }, 100);
    
    addSystemMessage(`File received: ${fileReceiveInfo.name} (${formatBytes(fileReceiveInfo.size)})`);
    updateStatus('Connected to peer');
    
    // Reset file transfer state
    fileReceiveInfo = null;
    transferProgress.classList.add('hidden');
}

// Add system message to chat
function addSystemMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'system-message text-sm py-1';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

// Add peer message to chat
function addPeerMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'peer-message p-2 rounded-lg max-w-[80%]';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

// Add my message to chat
function addMyMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'my-message p-2 rounded-lg max-w-[80%]';
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

// Hide chat and file interface when disconnected
function hideChatAndFileInterface() {
    chatWindow.classList.add('hidden');
    filePanel.classList.add('hidden');
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

// Initialize the application when the page loads
window.addEventListener('load', init);