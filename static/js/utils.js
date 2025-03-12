// Format bytes to human-readable format
export function formatBytes(bytes, decimals = 2) {
    if (bytes === 0) return '0 Bytes';
    
    const k = 1024;
    const dm = decimals < 0 ? 0 : decimals;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    
    return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i];
}

// Calculate MD5 hash of a file or blob
export function calculateMD5(file) {
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

// Show browser notification
export function showNotification(title, message) {
    if ('Notification' in window && Notification.permission === 'granted') {
        new Notification(title, { body: message });
    }
}

// Update page title with loading indicator
export function updateTitleWithSpinner(isLoading) {
    const baseTitle = 'P2P File Transfer';
    document.title = isLoading ? `↻ ${baseTitle}` : baseTitle;
}

// Set up browser notifications
export function setupNotifications() {
    if ('Notification' in window) {
        if (Notification.permission !== 'granted' && Notification.permission !== 'denied') {
            Notification.requestPermission();
        }
    }
}
