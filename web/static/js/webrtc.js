/**
 * P2PFTP WebRTC Implementation
 * This file handles the WebRTC connection and signaling
 */

class P2PConnection {
    constructor(logger) {
        // Configuration (will be updated after fetching server config)
        this.config = {
            iceServers: [
                {
                    urls: [
                        'stun:stun.l.google.com:19302',
                        'stun:stun1.l.google.com:19302',
                        'stun:stun2.l.google.com:19302',
                        'stun:stun3.l.google.com:19302',
                        'stun:stun4.l.google.com:19302'
                    ]
                }
            ],
            iceCandidatePoolSize: 10,
            iceTransportPolicy: 'all'
        };
        
        this.stunServersLoaded = false;

        // Channel configuration
        this.controlChannelConfig = {
            negotiated: true,
            id: 1,
            label: 'p2pftp-control',
            ordered: true,
            priority: 'high'
        };

        this.dataChannelConfig = {
            negotiated: true,
            id: 2,
            label: 'p2pftp-data',
            ordered: true,
            priority: 'high'  // CRITICAL FIX: Use high priority for file transfers
        };

        // Buffer size configuration as per protocol
        this.CONTROL_BUFFER_SIZE = 256 * 1024; // 256KB for control channel
        this.DATA_BUFFER_SIZE = 1024 * 1024; // 1MB for data channel

        // Chunk size constants as defined in the protocol
        this.MIN_CHUNK_SIZE = 4096;   // 4KB minimum
        this.DEFAULT_CHUNK_SIZE = 16384;  // 16KB default
        this.MAX_CHUNK_SIZE = 262144; // 256KB maximum

        // State
        this.peerConnection = null;
        this.signaler = null;
        this.controlChannel = null;
        this.dataChannel = null;
        this.token = null;
        this.peerToken = null;
        this.connected = false;
        this.isInitiator = false;
        this.maxChunkSize = this.DEFAULT_CHUNK_SIZE;
        this.negotiatedChunkSize = this.DEFAULT_CHUNK_SIZE;
        this.capabilitiesExchanged = false;
        this.capabilitiesExchangeTimeout = null;
        this.capabilitiesPromise = null;
        this.capabilitiesResolve = null;
        this.capabilitiesReject = null;
        this.logger = logger || console;
        this.pendingICECandidates = [];
        this.connectionAccepted = false;
        
        // Server disconnection state tracking
        this.serverConnected = false;
        this.p2pConnected = false;
        this.serverDisconnected = false;

        // Event handlers
        this.onStatusChange = null;
        this.onTokenReceived = null;
        this.onConnectionRequest = null;
        this.onMessage = null;
        this.onControlMessage = null;
        this.onDataMessage = null;
        this.onError = null;
        this.onPeerDisconnect = null;
    }

    /**
     * Fetch STUN server configuration from the server
     * @param {string} httpURL - The HTTP URL of the server
     * @returns {Promise} - Resolves when config is fetched
     */
    async fetchStunServers(httpURL) {
        try {
            // Only fetch once
            if (this.stunServersLoaded) {
                return;
            }

            // Ensure URL has proper format
            let baseURL = httpURL;
            if (!baseURL.startsWith('http')) {
                baseURL = 'https://' + baseURL;
            }

            const response = await fetch(baseURL + '/api/config');
            if (!response.ok) {
                throw new Error(`Failed to fetch config: ${response.status}`);
            }

            const data = await response.json();
            if (data.stunServers && Array.isArray(data.stunServers) && data.stunServers.length > 0) {
                this.config.iceServers = [
                    {
                        urls: data.stunServers
                    }
                ];
                this.logger.log('Loaded STUN servers from server:', data.stunServers);
            }
            
            this.stunServersLoaded = true;
        } catch (error) {
            this.logger.warn('Failed to fetch STUN servers from server, using defaults:', error);
            this.stunServersLoaded = true;
        }
    }

    /**
     * Connect to the signaling server
     * @param {string} serverURL - The URL of the signaling server
     * @returns {Promise} - Resolves when connected to the server
     */
    async connectToServer(serverURL) {
        try {
            // Validate server URL input
            if (!serverURL) {
                throw new Error('Server URL is required');
            }
            
            if (typeof serverURL !== 'string') {
                throw new Error('Server URL must be a string');
            }
            
            // Trim whitespace
            serverURL = serverURL.trim();
            
            if (!serverURL) {
                throw new Error('Server URL cannot be empty or whitespace only');
            }

            // Ensure URL has proper format
            if (!serverURL.startsWith('http')) {
                serverURL = 'https://' + serverURL;
            }

            // Fetch STUN servers from the server
            await this.fetchStunServers(serverURL);

            // Convert HTTP/HTTPS URL to WSS URL
            const wsURL = this._getWebSocketURL(serverURL);
            
            // Validate that we got a valid WebSocket URL
            if (!wsURL || !wsURL.startsWith('wss://')) {
                throw new Error('Failed to construct valid WebSocket URL from server URL');
            }
            
            this.logger.log('Connecting to signaling server:', wsURL);

            // Create WebSocket connection
            this.signaler = new WebSocket(wsURL);
            
            // Set up event handlers
            this.signaler.onopen = () => {
                this.logger.log('Connected to signaling server');
                this.serverConnected = true;
                if (this.onStatusChange) {
                    this.onStatusChange('Connected to signaling server');
                }
            };

            this.signaler.onclose = () => {
                this.logger.log('Disconnected from signaling server');
                this.serverConnected = false;
                if (this.onStatusChange) {
                    this.onStatusChange('Disconnected from signaling server');
                }
            };

            this.signaler.onerror = (error) => {
                this.logger.error('Signaling server error:', error);
                if (this.onError) {
                    this.onError('Signaling server error: ' + error);
                }
            };

            this.signaler.onmessage = (event) => {
                this._handleSignalingMessage(event.data);
            };

            // Wait for connection to open
            return new Promise((resolve, reject) => {
                const timeout = setTimeout(() => {
                    reject(new Error('Connection to signaling server timed out'));
                }, 10000);

                this.signaler.onopen = () => {
                    clearTimeout(timeout);
                    this.logger.log('Connected to signaling server');
                    this.serverConnected = true;
                    if (this.onStatusChange) {
                        this.onStatusChange('Connected to signaling server');
                    }
                    resolve();
                };

                this.signaler.onerror = (error) => {
                    clearTimeout(timeout);
                    this.logger.error('Signaling server error:', error);
                    if (this.onError) {
                        this.onError('Signaling server error: ' + error);
                    }
                    reject(error);
                };
            });
        } catch (error) {
            this.logger.error('Error connecting to server:', error);
            if (this.onError) {
                this.onError('Error connecting to server: ' + error.message);
            }
            throw error;
        }
    }

    /**
     * Initialize the WebRTC peer connection
     */
    initializePeerConnection() {
        try {
            // Create peer connection
            this.peerConnection = new RTCPeerConnection(this.config);
            
            // Set up event handlers
            this.peerConnection.onicecandidate = (event) => {
                if (event.candidate) {
                    this._handleICECandidate(event.candidate);
                }
            };

            this.peerConnection.oniceconnectionstatechange = () => {
                this.logger.log('ICE connection state:', this.peerConnection.iceConnectionState);
                if (this.onStatusChange) {
                    this.onStatusChange('Connection state: ' + this.peerConnection.iceConnectionState);
                }

                // Update connection status
                if (this.peerConnection.iceConnectionState === 'connected' ||
                    this.peerConnection.iceConnectionState === 'completed') {
                    this.connected = true;
                    this.p2pConnected = true;
                } else if (this.peerConnection.iceConnectionState === 'failed' ||
                           this.peerConnection.iceConnectionState === 'disconnected' ||
                           this.peerConnection.iceConnectionState === 'closed') {
                    this.connected = false;
                    this.p2pConnected = false;
                    
                    // Handle P2P failure after server disconnection
                    if (this.serverDisconnected) {
                        this._handleP2PFailureAfterServerDisconnect();
                    }
                }
            };

            this.peerConnection.ondatachannel = (event) => {
                this.logger.log('Data channel received:', event.channel.label);
                
                if (event.channel.label === 'p2pftp-control') {
                    this.controlChannel = event.channel;
                    this._setupControlChannel(this.controlChannel);
                } else if (event.channel.label === 'p2pftp-data') {
                    this.dataChannel = event.channel;
                    this._setupDataChannel(this.dataChannel);
                }
            };

            // Create data channels (pre-negotiated)
            this._createDataChannels();

            this.logger.log('Peer connection initialized');
        } catch (error) {
            this.logger.error('Error initializing peer connection:', error);
            if (this.onError) {
                this.onError('Error initializing peer connection: ' + error.message);
            }
            throw error;
        }
    }

    /**
     * Send a connection request to a peer
     * @param {string} peerToken - The token of the peer to connect to
     */
    connectToPeer(peerToken) {
        if (!this.signaler || this.signaler.readyState !== WebSocket.OPEN) {
            throw new Error('Not connected to signaling server');
        }

        if (!peerToken) {
            throw new Error('Peer token is required');
        }

        this.isInitiator = true;
        this.peerToken = peerToken;

        const message = {
            type: 'connect',
            peerToken: peerToken
        };

        this.signaler.send(JSON.stringify(message));
        this.logger.log('Sent connection request to peer:', peerToken);
        
        if (this.onStatusChange) {
            this.onStatusChange('Connection request sent to peer: ' + peerToken);
        }
    }

    /**
     * Accept a connection request from a peer
     * @param {string} peerToken - The token of the peer to accept
     */
    acceptConnection(peerToken) {
        if (!this.signaler || this.signaler.readyState !== WebSocket.OPEN) {
            throw new Error('Not connected to signaling server');
        }

        if (!peerToken) {
            throw new Error('Peer token is required');
        }

        this.peerToken = peerToken;
        this.connectionAccepted = true;

        const message = {
            type: 'accept',
            peerToken: peerToken
        };

        this.signaler.send(JSON.stringify(message));
        this.logger.log('Accepted connection from peer:', peerToken);
        
        if (this.onStatusChange) {
            this.onStatusChange('Connection accepted from peer: ' + peerToken);
        }
    }

    /**
     * Reject a connection request from a peer
     * @param {string} peerToken - The token of the peer to reject
     */
    rejectConnection(peerToken) {
        if (!this.signaler || this.signaler.readyState !== WebSocket.OPEN) {
            throw new Error('Not connected to signaling server');
        }

        if (!peerToken) {
            throw new Error('Peer token is required');
        }

        const message = {
            type: 'reject',
            peerToken: peerToken
        };

        this.signaler.send(JSON.stringify(message));
        this.logger.log('Rejected connection from peer:', peerToken);
        
        if (this.onStatusChange) {
            this.onStatusChange('Connection rejected from peer: ' + peerToken);
        }
    }

    /**
     * Send a chat message to the peer
     * @param {string} content - The message content
     */
    sendChatMessage(content) {
        if (!this.controlChannel || this.controlChannel.readyState !== 'open') {
            throw new Error('Control channel not open');
        }

        const message = {
            type: 'message',
            content: content
        };

        this.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent chat message:', content);
    }

    /**
     * Send capabilities to the peer
     */
    sendCapabilities() {
        if (!this.controlChannel || this.controlChannel.readyState !== 'open') {
            this.logger.warn('Control channel not open, cannot send capabilities');
            return;
        }

        // Create a promise for capabilities exchange
        this.capabilitiesPromise = new Promise((resolve, reject) => {
            this.capabilitiesResolve = resolve;
            this.capabilitiesReject = reject;
            
            // Set up 5-second timeout
            this.capabilitiesExchangeTimeout = setTimeout(() => {
                this.logger.error('Capabilities exchange timed out after 5 seconds');
                this.capabilitiesExchanged = false;
                if (this.capabilitiesReject) {
                    this.capabilitiesReject(new Error('Capabilities exchange timed out'));
                }
            }, 5000);
        });

        const message = {
            type: 'capabilities',
            maxChunkSize: this.maxChunkSize
        };

        this.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent capabilities, max chunk size:', this.maxChunkSize);
    }

    /**
     * Wait for capabilities exchange to complete
     * @returns {Promise} - Resolves when capabilities are exchanged
     */
    waitForCapabilitiesExchange() {
        if (this.capabilitiesExchanged) {
            return Promise.resolve();
        }
        
        if (!this.capabilitiesPromise) {
            return Promise.reject(new Error('Capabilities exchange not initiated'));
        }
        
        return this.capabilitiesPromise;
    }

    /**
     * Send capabilities acknowledgment to the peer
     * @param {number} negotiatedSize - The negotiated chunk size
     */
    sendCapabilitiesAck(negotiatedSize) {
        if (!this.controlChannel || this.controlChannel.readyState !== 'open') {
            this.logger.warn('Control channel not open, cannot send capabilities ack');
            return;
        }

        const message = {
            type: 'capabilities-ack',
            negotiatedChunkSize: negotiatedSize
        };

        this.controlChannel.send(JSON.stringify(message));
        this.logger.log('Sent capabilities acknowledgment, negotiated size:', negotiatedSize);
    }

    /**
     * Check if the connection is established
     * @returns {boolean} - True if connected, false otherwise
     */
    isConnected() {
        return this.p2pConnected &&
               this.controlChannel &&
               this.controlChannel.readyState === 'open' &&
               this.dataChannel &&
               this.dataChannel.readyState === 'open';
    }

    /**
     * Check if connected to signaling server
     * @returns {boolean} - True if connected to server, false otherwise
     */
    isServerConnected() {
        return this.serverConnected &&
               this.signaler &&
               this.signaler.readyState === WebSocket.OPEN;
    }

    /**
     * Check if can safely disconnect from server
     * @returns {boolean} - True if can disconnect, false otherwise
     */
    canDisconnectFromServer() {
        return this.isServerConnected() &&
               this.isConnected() &&
               this.capabilitiesExchanged &&
               !this.serverDisconnected;
    }

    /**
     * Disconnect from signaling server after P2P connection is established
     */
    disconnectFromServer() {
        // Check preconditions for safe disconnection
        if (!this.canDisconnectFromServer()) {
            this.logger.warn('Cannot disconnect from server: conditions not met');
            return false;
        }

        try {
            this.logger.log('Disconnecting from signaling server - P2P connection is stable');
            
            // Close WebSocket connection gracefully
            if (this.signaler && this.signaler.readyState === WebSocket.OPEN) {
                this.signaler.close(1000, 'P2P connection established');
            }
            
            // Clear server-related state
            this.signaler = null;
            this.serverDisconnected = true;
            this.serverConnected = false;
            
            // Update UI with status change
            if (this.onStatusChange) {
                this.onStatusChange('Disconnected from signaling server - P2P connection active');
            }
            
            this.logger.log('Successfully disconnected from signaling server');
            return true;
        } catch (error) {
            this.logger.error('Error disconnecting from server:', error);
            if (this.onError) {
                this.onError('Error disconnecting from server: ' + error.message);
            }
            return false;
        }
    }

    /**
     * Close the connection
     */
    close() {
        // Clear capabilities exchange timeout
        if (this.capabilitiesExchangeTimeout) {
            clearTimeout(this.capabilitiesExchangeTimeout);
            this.capabilitiesExchangeTimeout = null;
        }
        
        // Close data channels
        if (this.controlChannel) {
            this.controlChannel.close();
        }
        
        if (this.dataChannel) {
            this.dataChannel.close();
        }
        
        // Close peer connection
        if (this.peerConnection) {
            this.peerConnection.close();
        }
        
        // Close signaling connection
        if (this.signaler && this.signaler.readyState === WebSocket.OPEN) {
            this.signaler.close();
        }
        
        // Reset state
        this.connected = false;
        this.isInitiator = false;
        this.capabilitiesExchanged = false;
        this.connectionAccepted = false;
        this.capabilitiesPromise = null;
        this.capabilitiesResolve = null;
        this.capabilitiesReject = null;
        
        this.logger.log('Connection closed');
        
        if (this.onStatusChange) {
            this.onStatusChange('Connection closed');
        }
    }

    /**
     * Create the WebRTC data channels
     * @private
     */
    _createDataChannels() {
        try {
            // Create control channel with proper buffer size
            this.controlChannel = this.peerConnection.createDataChannel(
                this.controlChannelConfig.label,
                {
                    negotiated: this.controlChannelConfig.negotiated,
                    id: this.controlChannelConfig.id,
                    ordered: this.controlChannelConfig.ordered,
                    priority: this.controlChannelConfig.priority
                }
            );
            
            // Set buffer size for control channel (256KB)
            if (this.controlChannel.bufferedAmountLowThreshold !== undefined) {
                this.controlChannel.bufferedAmountLowThreshold = this.CONTROL_BUFFER_SIZE / 4; // 25% threshold
            }
            
            this._setupControlChannel(this.controlChannel);

            // Create data channel with proper buffer size
            this.dataChannel = this.peerConnection.createDataChannel(
                this.dataChannelConfig.label,
                {
                    negotiated: this.dataChannelConfig.negotiated,
                    id: this.dataChannelConfig.id,
                    ordered: this.dataChannelConfig.ordered,
                    priority: this.dataChannelConfig.priority
                }
            );
            
            // Set buffer size for data channel (1MB) with optimized threshold
            if (this.dataChannel.bufferedAmountLowThreshold !== undefined) {
                this.dataChannel.bufferedAmountLowThreshold = this.DATA_BUFFER_SIZE / 8; // 12.5% threshold for better flow
            }
            
            this._setupDataChannel(this.dataChannel);

            this.logger.log('Data channels created');
        } catch (error) {
            this.logger.error('Error creating data channels:', error);
            if (this.onError) {
                this.onError('Error creating data channels: ' + error.message);
            }
            throw error;
        }
    }

    /**
     * Set up the control channel
     * @param {RTCDataChannel} channel - The control channel
     * @private
     */
    _setupControlChannel(channel) {
        channel.onopen = () => {
            this.logger.log('Control channel opened');
            if (this.onStatusChange) {
                this.onStatusChange('Control channel opened');
            }
            
            // Send capabilities after channel is open
            this.sendCapabilities();
        };

        channel.onclose = () => {
            this.logger.log('Control channel closed');
            if (this.onStatusChange) {
                this.onStatusChange('Control channel closed');
            }
            
            // Trigger peer disconnect event if we were connected
            if (this.connected && this.onPeerDisconnect) {
                this.connected = false;
                this.onPeerDisconnect();
            }
        };

        channel.onerror = (error) => {
            this.logger.error('Control channel error:', error);
            if (this.onError) {
                this.onError('Control channel error: ' + error.message);
            }
        };

        channel.onmessage = (event) => {
            let data = event.data;
            
            try {
                // Try to parse as JSON
                if (typeof data === 'string') {
                    const jsonData = JSON.parse(data);
                    this.logger.log('Received control message:', jsonData.type);
                    
                    // DEBUG: Log flow control acks specifically
                    if (jsonData.type === 'flow-control-ack') {
                        this.logger.log('DEBUG: Received flow-control-ack:', jsonData);
                    }
                    
                    // Handle specific message types
                    switch (jsonData.type) {
                        case 'message':
                            if (this.onMessage) {
                                this.onMessage(jsonData.content);
                            }
                            break;
                        case 'capabilities':
                            this._handleCapabilities(jsonData);
                            break;
                        case 'capabilities-ack':
                            this._handleCapabilitiesAck(jsonData);
                            break;
                        default:
                            // Pass to general control handler
                            if (this.onControlMessage) {
                                this.onControlMessage(jsonData);
                            }
                            break;
                    }
                } else {
                    // Binary data on control channel
                    this.logger.warn('Received binary data on control channel');
                    if (this.onControlMessage) {
                        this.onControlMessage(data);
                    }
                }
            } catch (error) {
                this.logger.error('Error parsing control message:', error);
                // Still pass the raw data to the control handler
                if (this.onControlMessage) {
                    this.onControlMessage(data);
                }
            }
        };
    }

    /**
     * Set up the data channel
     * @param {RTCDataChannel} channel - The data channel
     * @private
     */
    _setupDataChannel(channel) {
        // Set binary type to arraybuffer
        channel.binaryType = 'arraybuffer';

        channel.onopen = () => {
            this.logger.log('Data channel opened');
            if (this.onStatusChange) {
                this.onStatusChange('Data channel opened');
            }
            
            // Send a test message to verify the channel is working
            const testData = new Uint8Array([0, 0, 0, 0, 0, 0, 0, 8, 1, 2, 3, 4, 5, 6, 7, 8]);
            try {
                channel.send(testData);
                this.logger.log('Test message sent successfully');
            } catch (error) {
                this.logger.error('Failed to send test message:', error);
            }
        };

        channel.onclose = () => {
            this.logger.log('Data channel closed');
            if (this.onStatusChange) {
                this.onStatusChange('Data channel closed');
            }
            
            // Trigger peer disconnect event if we were connected
            if (this.connected && this.onPeerDisconnect) {
                this.connected = false;
                this.onPeerDisconnect();
            }
        };

        channel.onerror = (error) => {
            this.logger.error('Data channel error:', error);
            if (this.onError) {
                this.onError('Data channel error: ' + error.message);
            }
        };

        channel.onmessage = (event) => {
            const data = event.data;
            
            if (data instanceof ArrayBuffer) {
                this.logger.log(`Received binary data: ${data.byteLength} bytes`);
                
                // Extract transfer ID, sequence number and size if possible
                if (data.byteLength >= 12) {
                    const view = new DataView(data);
                    const transferId = view.getUint32(0);
                    const sequence = view.getUint32(4);
                    const chunkSize = view.getUint32(8);
                    this.logger.log(`Data appears to be chunk ${sequence} for transfer ${transferId} with size ${chunkSize}`);
                } else if (data.byteLength >= 8) {
                    // Legacy format without transfer ID
                    const view = new DataView(data);
                    const sequence = view.getUint32(0);
                    const chunkSize = view.getUint32(4);
                    this.logger.log(`Data appears to be legacy chunk ${sequence} with size ${chunkSize}`);
                }
                
                // Pass to data handler
                if (this.onDataMessage) {
                    this.onDataMessage(data);
                }
            } else {
                this.logger.warn('Received non-binary data on data channel:', data);
                // Still try to pass to data handler
                if (this.onDataMessage) {
                    this.onDataMessage(data);
                }
            }
        };
    }

    /**
     * Handle capabilities message from peer
     * @param {Object} capabilities - The capabilities message
     * @private
     */
    _handleCapabilities(capabilities) {
        const peerMaxChunkSize = capabilities.maxChunkSize || this.DEFAULT_CHUNK_SIZE;
        
        this.logger.log('Received capabilities from peer, max chunk size:', peerMaxChunkSize);
        
        // Negotiate chunk size (minimum of both peers)
        const negotiatedSize = Math.min(this.maxChunkSize, peerMaxChunkSize);
        
        // Log if peer supports larger chunks than our maximum
        if (peerMaxChunkSize > this.maxChunkSize) {
            this.logger.log('Peer supports larger chunks than our maximum. Consider increasing our limit for better performance.');
        }
        
        // Update negotiated chunk size
        this.negotiatedChunkSize = negotiatedSize;
        
        // Send acknowledgment
        this.sendCapabilitiesAck(negotiatedSize);
        
        // Mark capabilities as exchanged
        this.capabilitiesExchanged = true;
        
        // Clear timeout and resolve promise
        if (this.capabilitiesExchangeTimeout) {
            clearTimeout(this.capabilitiesExchangeTimeout);
            this.capabilitiesExchangeTimeout = null;
        }
        
        if (this.capabilitiesResolve) {
            this.capabilitiesResolve();
            this.capabilitiesResolve = null;
            this.capabilitiesReject = null;
        }
        
        this.logger.log('Negotiated chunk size:', negotiatedSize);
        
        if (this.onStatusChange) {
            this.onStatusChange('Negotiated chunk size: ' + negotiatedSize + ' bytes');
        }
        
        // Check for P2P stability and trigger disconnection
        this._checkAndDisconnectFromServer();
    }

    /**
     * Handle capabilities acknowledgment from peer
     * @param {Object} ack - The capabilities acknowledgment message
     * @private
     */
    _handleCapabilitiesAck(ack) {
        const negotiatedSize = ack.negotiatedChunkSize || this.DEFAULT_CHUNK_SIZE;
        
        this.logger.log('Received capabilities acknowledgment, negotiated size:', negotiatedSize);
        
        // Update negotiated chunk size
        this.negotiatedChunkSize = negotiatedSize;
        
        // Mark capabilities as exchanged
        this.capabilitiesExchanged = true;
        
        // Clear timeout and resolve promise
        if (this.capabilitiesExchangeTimeout) {
            clearTimeout(this.capabilitiesExchangeTimeout);
            this.capabilitiesExchangeTimeout = null;
        }
        
        if (this.capabilitiesResolve) {
            this.capabilitiesResolve();
            this.capabilitiesResolve = null;
            this.capabilitiesReject = null;
        }
        
        if (this.onStatusChange) {
            this.onStatusChange('Negotiated chunk size: ' + negotiatedSize + ' bytes');
        }
        
        // Check for P2P stability and trigger disconnection
        this._checkAndDisconnectFromServer();
    }

    /**
     * Handle ICE candidate
     * @param {RTCIceCandidate} candidate - The ICE candidate
     * @private
     */
    _handleICECandidate(candidate) {
        // Ignore candidates after server disconnection
        if (this.serverDisconnected) {
            this.logger.debug('Ignoring ICE candidate - server disconnected');
            return;
        }
        
        // If we don't have a peer token or connection is not accepted, buffer the candidate
        if (!this.peerToken || !this.connectionAccepted) {
            this.logger.log('Buffering ICE candidate until connection is accepted');
            this.pendingICECandidates.push(candidate);
            return;
        }
        
        // Send the ICE candidate to the peer
        this._sendICECandidate(candidate);
    }

    /**
     * Send ICE candidate to peer
     * @param {RTCIceCandidate} candidate - The ICE candidate
     * @private
     */
    _sendICECandidate(candidate) {
        // Don't send ICE candidates after server disconnection
        if (this.serverDisconnected) {
            this.logger.debug('Not sending ICE candidate - server disconnected');
            return;
        }
        
        if (!this.signaler || this.signaler.readyState !== WebSocket.OPEN) {
            this.logger.warn('Cannot send ICE candidate: not connected to signaling server');
            return;
        }
        
        if (!this.peerToken) {
            this.logger.warn('Cannot send ICE candidate: peer token is empty');
            return;
        }
        
        const message = {
            type: 'ice',
            peerToken: this.peerToken,
            ice: JSON.stringify(candidate)
        };
        
        this.signaler.send(JSON.stringify(message));
        this.logger.log('Sent ICE candidate to peer');
    }

    /**
     * Send buffered ICE candidates
     */
    sendBufferedICECandidates() {
        if (this.pendingICECandidates.length > 0) {
            this.logger.log(`Sending ${this.pendingICECandidates.length} buffered ICE candidates`);
            
            for (const candidate of this.pendingICECandidates) {
                this._sendICECandidate(candidate);
            }
            
            this.pendingICECandidates = [];
        }
    }

    /**
     * Handle signaling message
     * @param {string} data - The message data
     * @private
     */
    _handleSignalingMessage(data) {
        try {
            const message = JSON.parse(data);
            this.logger.log('Received signaling message:', message.type, 'Full message:', message);
            
            switch (message.type) {
                case 'token':
                    this.token = message.token;
                    this.logger.log('Assigned token:', this.token);
                    if (this.onTokenReceived) {
                        this.onTokenReceived(this.token);
                    }
                    break;
                    
                case 'error':
                    this.logger.error('Server error:', message.sdp);
                    if (this.onError) {
                        this.onError('Server error: ' + message.sdp);
                    }
                    break;
                    
                case 'request':
                    const requestToken = message.token;
                    this.logger.log('Connection request from:', requestToken);
                    this.logger.debug('Connection request details - onConnectionRequest exists:', !!this.onConnectionRequest);
                    if (this.onConnectionRequest) {
                        this.onConnectionRequest(requestToken);
                    } else {
                        this.logger.error('onConnectionRequest handler not set!');
                    }
                    break;
                    
                case 'accepted':
                    this.peerToken = message.token;
                    this.connectionAccepted = true;
                    this.logger.log('Connection accepted by:', this.peerToken);
                    
                    if (this.onStatusChange) {
                        this.onStatusChange('Connection accepted by: ' + this.peerToken);
                    }
                    
                    // If this peer is the initiator, create and send the offer now
                    if (this.isInitiator) {
                        this._createAndSendOffer();
                    }
                    break;
                    
                case 'rejected':
                    this.logger.log('Connection rejected by:', message.token);
                    if (this.onStatusChange) {
                        this.onStatusChange('Connection rejected by: ' + message.token);
                    }
                    break;
                    
                case 'offer':
                    this.logger.log('Received offer from:', message.token);
                    
                    // Make sure we have the peer token set
                    if (!this.peerToken) {
                        this.peerToken = message.token;
                        this.logger.log('Setting peer token to:', this.peerToken);
                    }
                    
                    // Parse the offer
                    const offer = JSON.parse(message.sdp);
                    
                    // Set remote description
                    this.peerConnection.setRemoteDescription(new RTCSessionDescription(offer))
                        .then(() => {
                            // Create answer
                            return this.peerConnection.createAnswer();
                        })
                        .then(answer => {
                            // Set local description
                            return this.peerConnection.setLocalDescription(answer);
                        })
                        .then(() => {
                            // Send answer
                            this._sendAnswer(this.peerConnection.localDescription);
                        })
                        .catch(error => {
                            this.logger.error('Error handling offer:', error);
                            if (this.onError) {
                                this.onError('Error handling offer: ' + error.message);
                            }
                        });
                    break;
                    
                case 'answer':
                    this.logger.log('Received answer from:', message.token);
                    const answer = JSON.parse(message.sdp);
                    this.peerConnection.setRemoteDescription(new RTCSessionDescription(answer))
                        .catch(error => {
                            this.logger.error('Error setting remote description:', error);
                            if (this.onError) {
                                this.onError('Error setting remote description: ' + error.message);
                            }
                        });
                    break;
                    
                case 'ice':
                    this.logger.log('Received ICE candidate from:', message.token);
                    const candidate = JSON.parse(message.ice);
                    this.peerConnection.addIceCandidate(new RTCIceCandidate(candidate))
                        .catch(error => {
                            this.logger.error('Error adding ICE candidate:', error);
                            if (this.onError) {
                                this.onError('Error adding ICE candidate: ' + error.message);
                            }
                        });
                    break;
            }
        } catch (error) {
            this.logger.error('Error handling signaling message:', error);
            if (this.onError) {
                this.onError('Error handling signaling message: ' + error.message);
            }
        }
    }

    /**
     * Check P2P connection stability and disconnect from server if appropriate
     * @private
     */
    _checkAndDisconnectFromServer() {
        // Check if P2P connection is stable and capabilities are exchanged
        if (this.canDisconnectFromServer()) {
            // Use a short delay to ensure P2P connection is fully stable
            setTimeout(() => {
                if (this.canDisconnectFromServer()) {
                    this.disconnectFromServer();
                }
            }, 2000); // 2 second delay to ensure stability
        }
    }

    /**
     * Handle P2P connection failure after server disconnection
     * @private
     */
    _handleP2PFailureAfterServerDisconnect() {
        this.logger.error('P2P connection failed after server disconnection');
        
        if (this.onError) {
            this.onError('P2P connection lost. Please reconnect to the server to establish a new connection.');
        }
        
        if (this.onStatusChange) {
            this.onStatusChange('P2P connection lost - Server connection unavailable for reconnection');
        }
    }

    /**
     * Create and send an offer
     * @private
     */
    _createAndSendOffer() {
        this.logger.log('Creating and sending offer');
        
        this.peerConnection.createOffer()
            .then(offer => {
                return this.peerConnection.setLocalDescription(offer);
            })
            .then(() => {
                this._sendOffer(this.peerConnection.localDescription);
                
                // Send any buffered ICE candidates
                this.sendBufferedICECandidates();
            })
            .catch(error => {
                this.logger.error('Error creating offer:', error);
                if (this.onError) {
                    this.onError('Error creating offer: ' + error.message);
                }
            });
    }

    /**
     * Send an offer to the peer
     * @param {RTCSessionDescription} offer - The offer
     * @private
     */
    _sendOffer(offer) {
        if (!this.signaler || this.signaler.readyState !== WebSocket.OPEN) {
            throw new Error('Not connected to signaling server');
        }
        
        if (!this.peerToken) {
            throw new Error('Peer token is required');
        }
        
        const message = {
            type: 'offer',
            peerToken: this.peerToken,
            sdp: JSON.stringify(offer)
        };
        
        this.signaler.send(JSON.stringify(message));
        this.logger.log('Sent offer to peer');
    }

    /**
     * Send an answer to the peer
     * @param {RTCSessionDescription} answer - The answer
     * @private
     */
    _sendAnswer(answer) {
        if (!this.signaler || this.signaler.readyState !== WebSocket.OPEN) {
            throw new Error('Not connected to signaling server');
        }
        
        if (!this.peerToken) {
            throw new Error('Peer token is required');
        }
        
        const message = {
            type: 'answer',
            peerToken: this.peerToken,
            sdp: JSON.stringify(answer)
        };
        
        this.signaler.send(JSON.stringify(message));
        this.logger.log('Sent answer to peer');
    }

    /**
     * Convert HTTP/HTTPS URL to WSS URL
     * @param {string} httpURL - The HTTP/HTTPS URL
     * @returns {string} - The WSS URL
     * @private
     */
    _getWebSocketURL(httpURL) {
        // Validate input
        if (!httpURL || typeof httpURL !== 'string') {
            throw new Error('Invalid URL: URL must be a non-empty string');
        }
        
        // Trim whitespace
        httpURL = httpURL.trim();
        
        if (!httpURL) {
            throw new Error('Invalid URL: URL cannot be empty or whitespace only');
        }
        
        this.logger.debug('Original server URL:', httpURL);
        
        // Handle URLs without protocol
        if (!httpURL.includes('://')) {
            // Extract hostname and port if present
            let hostname = httpURL;
            let port = '443'; // default port
            
            if (httpURL.includes(':')) {
                const parts = httpURL.split(':');
                hostname = parts[0];
                port = parts[1] || '443';
            }
            
            // Validate hostname
            if (!hostname || !this._isValidHostname(hostname)) {
                throw new Error(`Invalid hostname: ${hostname}`);
            }
            
            // For localhost, use ws:// instead of wss:// for testing
            const protocol = (hostname === 'localhost' || hostname === '127.0.0.1') ? 'ws' : 'wss';
            const wsPort = (hostname === 'localhost' || hostname === '127.0.0.1') ? (port || '8090') : (port || '443');
            
            const wsURL = `${protocol}://${hostname}:${wsPort}/ws`;
            this.logger.debug('Constructed WebSocket URL (no protocol):', wsURL);
            return wsURL;
        }
        
        // Convert HTTP/HTTPS to WS/WSS
        let wsURL = httpURL.replace('http:', 'ws:').replace('https:', 'wss:').replace('ws:', 'ws:').replace('wss:', 'wss:');
        
        this.logger.debug('Converted WebSocket URL before parsing:', wsURL);
        
        // Parse URL to ensure correct port and path
        try {
            const url = new URL(wsURL);
            
            // Validate hostname
            if (!this._isValidHostname(url.hostname)) {
                throw new Error(`Invalid hostname: ${url.hostname}`);
            }
            
            // For localhost, keep the original port, otherwise use 443
            if (url.hostname === 'localhost' || url.hostname === '127.0.0.1') {
                // Keep existing port for localhost, ensure /ws path
                if (!url.port) {
                    url.port = '8090'; // default for localhost testing
                }
            } else {
                // For production, use port 443
                url.port = '443';
            }
            
            url.pathname = '/ws';
            const finalURL = url.toString();
            this.logger.debug('Final WebSocket URL:', finalURL);
            return finalURL;
        } catch (error) {
            this.logger.error('Error parsing URL:', error);
            throw new Error(`Failed to parse URL: ${error.message}`);
        }
    }
    
    /**
     * Validate hostname format
     * @param {string} hostname - The hostname to validate
     * @returns {boolean} - True if valid, false otherwise
     * @private
     */
    _isValidHostname(hostname) {
        if (!hostname || typeof hostname !== 'string') {
            return false;
        }
        
        // Basic hostname validation
        const hostnameRegex = /^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*$/;
        
        // Check length (max 253 characters)
        if (hostname.length > 253) {
            return false;
        }
        
        // Check against regex
        return hostnameRegex.test(hostname);
    }
}