import { formatBytes, showNotification, updateTitleWithSpinner } from '/static/js/utils.js';

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

// UI state
let selectedFile = null;

// Setup UI event listeners
export function setupEventListeners(handlers) {
    const {
        handleConnect,
        handleAccept,
        handleReject,
        handleSendMessage,
        handleFileSelected,
        handleSendFile,
        disconnectFromPeer
    } = handlers;

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
        const fullUrl = `${currentUrl}?token=${encodeURIComponent(myTokenInput.value)}`;
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
    // Setup disconnect buttons
    document.getElementById('disconnect-btn').addEventListener('click', disconnectFromPeer);
    document.getElementById('disconnect-status-btn').addEventListener('click', disconnectFromPeer);

    // Setup connect button
    connectBtn.addEventListener('click', () => {
        if (handlers.isConnected()) {
            disconnectFromPeer();
        } else {
            const token = peerTokenInput.value.trim();
            if (token && token !== myTokenInput.value) {
                handleConnect(token);
            } else {
                addSystemMessage('Please enter a valid peer token');
            }
        }
    });

    // Accept/Reject buttons
    acceptBtn.addEventListener('click', handleAccept);
    rejectBtn.addEventListener('click', handleReject);

    // Send message
    sendBtn.addEventListener('click', handleSendMessage);
    messageInput.addEventListener('keypress', (event) => {
        if (event.key === 'Enter') {
            handleSendMessage();
        }
    });

    // File handling
    fileInput.addEventListener('change', (event) => {
        selectedFile = event.target.files[0];
        if (selectedFile) {
            fileName.textContent = selectedFile.name;
            fileSize.textContent = formatBytes(selectedFile.size);
            fileInfo.classList.remove('hidden');
            sendFileBtn.disabled = false;
            sendFileBtn.classList.remove('opacity-50');
            handleFileSelected(selectedFile);
        }
    });

    sendFileBtn.addEventListener('click', () => {
        if (selectedFile && handlers.isConnected()) {
            handleSendFile(selectedFile);
        }
    });
}

// UI update functions
export function updateConnectionStatus(status, connectedPeerToken = null) {
    if (connectedPeerToken) {
        connectionPanel.classList.add('hidden');
        connectionStatus.textContent = `Connected to ${connectedPeerToken}`;
        document.getElementById('disconnect-status-btn').classList.remove('hidden');
    } else {
        connectionPanel.classList.remove('hidden');
        connectionStatus.textContent = status;
        document.getElementById('disconnect-status-btn').classList.add('hidden');
    }
}

export function showConnectionRequest(token) {
    requesterTokenSpan.textContent = token;
    requestPanel.classList.remove('hidden');
    showNotification('Connection Request', `Peer ${token} wants to connect with you`);
    updateTitleWithSpinner(true);
}

export function hideConnectionRequest() {
    requestPanel.classList.add('hidden');
}

export function showChatAndFileInterface() {
    chatWindow.classList.remove('hidden');
    filePanel.classList.remove('hidden');
}

export function hideChatAndFileInterface() {
    chatWindow.classList.add('hidden');
    filePanel.classList.add('hidden');
}

export function updateConnectButton(isConnecting, isConnected) {
    if (isConnected) {
        connectBtn.textContent = 'Connected - Disconnect?';
        connectBtn.classList.remove('bg-yellow-500', 'hover:bg-yellow-600');
        connectBtn.classList.add('bg-red-500', 'hover:bg-red-600');
        peerTokenInput.disabled = true;
    } else if (isConnecting) {
        connectBtn.textContent = 'Connecting...';
        connectBtn.classList.remove('bg-green-500', 'hover:bg-green-600');
        connectBtn.classList.add('bg-yellow-500', 'hover:bg-yellow-600');
    } else {
        connectBtn.textContent = 'Connect';
        connectBtn.classList.remove('bg-yellow-500', 'hover:bg-yellow-600', 'bg-red-500', 'hover:bg-red-600');
        connectBtn.classList.add('bg-green-500', 'hover:bg-green-600');
        peerTokenInput.disabled = false;
    }
}

export function resetFileInterface() {
    transferProgress.classList.add('hidden');
    fileInfo.classList.add('hidden');
    fileInput.value = '';
    selectedFile = null;
    sendFileBtn.disabled = true;
    sendFileBtn.classList.add('opacity-50');
}

export function updateTransferProgress(percentage, status) {
    progressBar.style.width = `${percentage}%`;
    transferPercentage.textContent = `${percentage}%`;
    transferStatus.textContent = status;
    transferProgress.classList.remove('hidden');
}

export function hideTransferProgress() {
    transferProgress.classList.add('hidden');
}

// Message display functions
export function addSystemMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'text-gray-500 dark:text-gray-400 text-sm py-1 italic';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

export function addPeerMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'bg-gray-100 dark:bg-gray-700 p-2 rounded-lg max-w-[80%]';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

export function addMyMessage(message) {
    const messageElement = document.createElement('div');
    messageElement.className = 'bg-blue-100 dark:bg-blue-900 p-2 rounded-lg max-w-[80%] ml-auto';
    messageElement.textContent = message;
    messagesContainer.appendChild(messageElement);
    messagesContainer.scrollTop = messagesContainer.scrollHeight;
}

export function clearMessageInput() {
    messageInput.value = '';
}

export function setMyToken(token) {
    myTokenInput.value = token;
}

export function getMessageInputValue() {
    return messageInput.value.trim();
}

export function addFileDownloadMessage(fileInfo, downloadUrl) {
    const messageElement = document.createElement('div');
    messageElement.className = 'text-gray-500 dark:text-gray-400 text-sm py-1 italic';
    
    const downloadLink = document.createElement('a');
    downloadLink.href = downloadUrl;
    downloadLink.download = fileInfo.name;
    downloadLink.className = 'text-blue-500 hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300 underline';
    downloadLink.textContent = `Download ${fileInfo.name} (${formatBytes(fileInfo.size)})`;
    
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
}
