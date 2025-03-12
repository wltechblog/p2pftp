// WebRTC configuration
export const CHUNK_SIZE = 65536; // 64KB chunks for better performance
export const PROGRESS_UPDATE_INTERVAL = 200; // Update progress every 200ms
export const WS_URL = `wss://${window.location.host}/ws`;
export const BYTES_PER_SEC_SMOOTHING = 0.1; // EMA smoothing factor for transfer rate

// WebRTC ICE configuration
export const RTC_CONFIG = {
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
