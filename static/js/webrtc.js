import { RTC_CONFIG, WS_URL } from '/static/js/config.js';
import * as ui from '/static/js/ui.js';

// WebRTC connection variables
let peerConnection;
let dataChannel;
let websocket;
let myToken = '';
let peerToken = '';
let isConnecting = false;

// Initialize WebRTC functionality
export function init() {
    setupWebSocket();
    ui.setupEventListeners({
        handleConnect: connectToPeer,
        handleAccept: acceptConnection,
        handleReject: rejectConnection,
        handleSendMessage: sendMessage,
        handleFileSelected: () => {}, // No special handling needed for file selection
        handleSendFile: (file) => {
            if (file && dataChannel?.readyState === 'open') {
                import('/static/js/filetransfer.js').then(module => {
                    module.sendFile(file);
                });
            }
        },
        disconnectFromPeer,
        isConnected: () => dataChannel?.readyState === 'open'
    });
}

// Set up WebSocket connection
function setupWebSocket() {
    websocket = new WebSocket(WS_URL);

    websocket.onopen = () => {
        ui.addSystemMessage('Connected to server');
        ui.updateConnectionStatus('Connected to server');
    };

    websocket.onclose = () => {
        ui.addSystemMessage('Disconnected from server');
        ui.updateConnectionStatus('Disconnected from server');
    };

    websocket.onerror = (error) => {
        ui.addSystemMessage('WebSocket error: ' + error);
        ui.updateConnectionStatus('Connection error');
    };

    websocket.onmessage = (event) => {
        const message = JSON.parse(event.data);
        handleSignalingMessage(message);
    };
}

// Handle signaling messages from the server
function handleSignalingMessage(message) {
    switch (message.type) {
        case 'token':
            myToken = message.token;
            ui.setMyToken(myToken);
            ui.addSystemMessage(`Your token: ${myToken}`);

            // Handle URL parameter for peer token
            const urlParams = new URLSearchParams(window.location.search);
            const peerTokenFromUrl = urlParams.get('token');
            if (peerTokenFromUrl && peerTokenFromUrl !== myToken) {
                connectToPeer(peerTokenFromUrl);
            }
            break;
            
        case 'request':
            ui.showConnectionRequest(message.token);
            break;
            
        case 'accepted':
            ui.addSystemMessage(`Peer ${message.token} accepted your connection request`);
            peerToken = message.token;
            initiatePeerConnection(true); // Initiator
            break;
            
        case 'rejected':
            ui.addSystemMessage(`Peer ${message.token} rejected your connection request`);
            resetConnectionState();
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
            ui.addSystemMessage(`Error: ${message.error}`);
            break;
    }
}

// Send a signaling message through the WebSocket
function sendSignalingMessage(message) {
    if (websocket.readyState === WebSocket.OPEN) {
        websocket.send(JSON.stringify(message));
    } else {
        ui.addSystemMessage('WebSocket is not connected');
    }
}

// Initialize WebRTC peer connection
function initiatePeerConnection(isInitiator) {
    const startTime = Date.now();
    console.debug('[WebRTC] Initiating peer connection...');

    peerConnection = new RTCPeerConnection(RTC_CONFIG);

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
        }
    };

    // Monitor ICE connection state
    peerConnection.oniceconnectionstatechange = () => {
        const state = peerConnection.iceConnectionState;
        const duration = Date.now() - startTime;
        console.debug(`[WebRTC] ICE state '${state}' after ${duration}ms`);
        ui.addSystemMessage(`ICE Connection State: ${state}`);
        
        if (state === 'failed' || state === 'disconnected') {
            console.warn('[WebRTC] Connection problems detected');
            ui.addSystemMessage('Connection problems detected, attempting recovery...');
            
            if (peerConnection && !isConnectionDead()) {
                try {
                    console.debug('[WebRTC] Attempting connection recovery');
                    peerConnection.restartIce();
                    
                    setTimeout(() => {
                        if (peerConnection && peerConnection.iceConnectionState === 'failed') {
                            console.debug('[WebRTC] Recovery timeout, disconnecting');
                            disconnectFromPeer();
                        }
                    }, 5000);
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
        ui.updateConnectionStatus(`WebRTC: ${state}`);
        
        if (state === 'failed') {
            ui.addSystemMessage('Connection failed. You may need to reconnect.');
            disconnectFromPeer();
        }
    };

    // Create data channel before offer/answer exchange
    const ordered = true;
    const maxRetransmits = 30;
    const negotiated = true;
    const id = 1;
    const dataChannel = peerConnection.createDataChannel('p2pftp', {
        ordered,
        maxRetransmits,
        negotiated,
        id
    });
    setupDataChannel(dataChannel);
    
    // Create offer if initiator
    if (isInitiator) {
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
                ui.addSystemMessage(`Error creating offer: ${error}`);
            });
    }
}

// Set up data channel event handlers
function setupDataChannel(channel) {
    dataChannel = channel;
    
    console.debug(`[WebRTC] Setting up data channel (ID: ${channel.id}, Label: ${channel.label})`);
    console.debug(`[WebRTC] Channel config - Ordered: ${channel.ordered}, MaxRetransmits: ${channel.maxRetransmits}`);
    console.debug(`[WebRTC] Channel negotiated: ${channel.negotiated}, Protocol: ${channel.protocol}`);

    dataChannel.onopen = () => {
        isConnecting = false;
        console.debug(`[WebRTC] Data channel opened (State: ${channel.readyState})`);
        ui.addSystemMessage('Peer connection established');
        ui.updateConnectionStatus('Connected to peer');
        ui.showChatAndFileInterface();
        ui.updateConnectButton(false, true);
    };
    
    dataChannel.onclose = () => {
        console.debug(`[WebRTC] Data channel closed (Last state: ${channel.readyState})`);
        ui.addSystemMessage('Peer connection closed');
        ui.updateConnectionStatus('Disconnected from peer');
        ui.hideChatAndFileInterface();
        resetConnectionState();
    };
    
    dataChannel.onerror = (error) => {
        console.error(`[WebRTC] Data channel error:`, error);
        ui.addSystemMessage(`Data channel error: ${error}`);
    };
    
    // Initialize file transfer and set up message handling
    import('/static/js/filetransfer.js').then(module => {
        module.init();
        dataChannel.onmessage = (event) => {
            window.dispatchEvent(new CustomEvent('datachannel-message', { detail: event }));
        };
    });
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
            ui.addSystemMessage(`Error handling offer: ${error}`);
        });
}

// Handle WebRTC answer
function handleAnswer(message) {
    const answer = JSON.parse(message.sdp);
    
    peerConnection.setRemoteDescription(new RTCSessionDescription(answer))
        .catch(error => {
            ui.addSystemMessage(`Error handling answer: ${error}`);
        });
}

// Handle ICE candidate
function handleICECandidate(message) {
    const candidate = JSON.parse(message.ice);
    
    peerConnection.addIceCandidate(new RTCIceCandidate(candidate))
        .catch(error => {
            ui.addSystemMessage(`Error adding ICE candidate: ${error}`);
        });
}

// Connect to peer
function connectToPeer(token) {
    if (!isConnecting) {
        isConnecting = true;
        peerToken = token;
        ui.updateConnectButton(true, false);
        sendSignalingMessage({
            type: 'connect',
            peerToken: token
        });
        ui.addSystemMessage(`Connection request sent to peer: ${token}`);
    }
}

// Accept connection request
function acceptConnection() {
    peerToken = document.getElementById('requester-token').textContent;
    sendSignalingMessage({
        type: 'accept',
        peerToken: peerToken
    });
    
    initiatePeerConnection(false); // Not the initiator
    ui.hideConnectionRequest();
    ui.updateConnectionStatus('Connecting to peer...');
}

// Reject connection request
function rejectConnection() {
    const requesterToken = document.getElementById('requester-token').textContent;
    sendSignalingMessage({
        type: 'reject',
        peerToken: requesterToken
    });
    ui.hideConnectionRequest();
}

// Send a chat message
function sendMessage() {
    const message = ui.getMessageInputValue();
    if (message && dataChannel?.readyState === 'open') {
        ui.addMyMessage(message);
        
        dataChannel.send(JSON.stringify({
            type: 'message',
            content: message
        }));
        
        ui.clearMessageInput();
    }
}

// Helper to check if connection is irrecoverable
function isConnectionDead() {
    return !peerConnection || 
           peerConnection.signalingState === 'closed' ||
           (peerConnection.iceConnectionState === 'failed' && 
            peerConnection.connectionState === 'failed');
}

// Disconnect from peer
export function disconnectFromPeer() {
    if (dataChannel) {
        dataChannel.close();
        dataChannel = null;
    }

    if (peerConnection) {
        peerConnection.close();
        peerConnection = null;
    }

    peerToken = '';
    resetConnectionState();
}

// Reset connection state
function resetConnectionState() {
    ui.updateConnectionStatus('Disconnected');
    ui.updateConnectButton(false, false);
    ui.resetFileInterface();
}

// Get data channel for file transfer
export function getDataChannel() {
    return dataChannel;
}
