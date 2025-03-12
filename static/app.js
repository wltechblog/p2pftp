import { setupNotifications } from './js/utils.js';
import * as webrtc from './js/webrtc.js';
import * as filetransfer from './js/filetransfer.js';

// Initialize the application
function init() {
    setupNotifications();
    webrtc.init();
    filetransfer.init();
}

// Initialize the application when the page loads
window.addEventListener('load', init);
