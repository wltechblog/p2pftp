package transfer

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

const (
defaultChunkSize = 16384
headerSize       = 8 // 4 bytes sequence + 4 bytes length
defaultTimeout   = 3 * time.Second
defaultWindow    = 64
)

// FileInfo represents metadata about a file
type FileInfo struct {
Name   string `json:"name"`
Size   int64  `json:"size"`
MD5    string `json:"md5"`
Chunks int    `json:"chunks"`
}

func (t *Transfer) Info() *FileInfo {
return t.info
}

// ChunkInfo represents metadata about a chunk
type ChunkInfo struct {
Sequence    int   `json:"sequence"`
TotalChunks int   `json:"totalChunks"`
Size        int32 `json:"size"`
}

// Transfer handles file transfers using the sliding window protocol
type Transfer struct {
info         *FileInfo
file         *os.File
chunkSize    int32
window       int
timeout      time.Duration
chunks       map[int][]byte
missing      map[int]bool
debugLog     *log.Logger
sendChan     chan []byte
ackChan      chan int
retransmitCh chan []int
mu           sync.RWMutex
}

// NewSender creates a new file transfer sender
func NewSender(filepath string, chunkSize int32, debug *log.Logger) (*Transfer, error) {
file, err := os.Open(filepath)
if err != nil {
return nil, fmt.Errorf("failed to open file: %v", err)
}

// Calculate MD5 hash
hash := md5.New()
if _, err := io.Copy(hash, file); err != nil {
file.Close()
return nil, fmt.Errorf("failed to calculate MD5: %v", err)
}

// Get file info
info, err := file.Stat()
if err != nil {
file.Close()
return nil, fmt.Errorf("failed to get file info: %v", err)
}

// Seek back to start
if _, err := file.Seek(0, 0); err != nil {
file.Close()
return nil, fmt.Errorf("failed to seek file: %v", err)
}

if chunkSize == 0 {
chunkSize = defaultChunkSize
}

totalChunks := (int(info.Size()) + int(chunkSize) - 1) / int(chunkSize)

t := &Transfer{
info: &FileInfo{
Name:   info.Name(),
Size:   info.Size(),
MD5:    fmt.Sprintf("%x", hash.Sum(nil)),
Chunks: totalChunks,
},
file:         file,
chunkSize:    chunkSize,
window:       defaultWindow,
timeout:      defaultTimeout,
chunks:       make(map[int][]byte),
missing:      make(map[int]bool),
debugLog:     debug,
sendChan:     make(chan []byte, defaultWindow),
ackChan:      make(chan int, defaultWindow),
retransmitCh: make(chan []int, 1),
}

return t, nil
}

// NewReceiver creates a new file transfer receiver
func NewReceiver(info *FileInfo, filepath string, debug *log.Logger) (*Transfer, error) {
file, err := os.Create(filepath)
if err != nil {
return nil, fmt.Errorf("failed to create file: %v", err)
}

t := &Transfer{
info:         info,
file:         file,
chunkSize:    defaultChunkSize,
chunks:       make(map[int][]byte),
missing:      make(map[int]bool),
debugLog:     debug,
retransmitCh: make(chan []int, 1),
}

// Initialize missing chunks
for i := 0; i < info.Chunks; i++ {
t.missing[i] = true
}

return t, nil
}

// Start begins the file transfer
func (t *Transfer) Start() error {
t.debugLog.Printf("Starting transfer of %s (%d bytes, %d chunks)",
t.info.Name, t.info.Size, t.info.Chunks)

go t.processAcknowledgements()
go t.handleRetransmissions()

return nil
}

// SendChunk sends a chunk of data
func (t *Transfer) SendChunk(sequence int) error {
t.mu.RLock()
defer t.mu.RUnlock()

// Calculate chunk size
size := t.chunkSize
if sequence == t.info.Chunks-1 {
remaining := t.info.Size % int64(t.chunkSize)
if remaining > 0 {
size = int32(remaining)
}
}

// Create buffer for chunk header and data
buf := make([]byte, headerSize+size)

// Write sequence number (4 bytes)
binary.BigEndian.PutUint32(buf[0:4], uint32(sequence))

// Write chunk size (4 bytes)
binary.BigEndian.PutUint32(buf[4:8], uint32(size))

// Read chunk data
offset := int64(sequence) * int64(t.chunkSize)
if _, err := t.file.ReadAt(buf[headerSize:], offset); err != nil && err != io.EOF {
return fmt.Errorf("failed to read chunk %d: %v", sequence, err)
}

t.debugLog.Printf("Sending chunk %d/%d (%d bytes)",
sequence+1, t.info.Chunks, size)

select {
case t.sendChan <- buf:
return nil
case <-time.After(t.timeout):
return fmt.Errorf("send timeout for chunk %d", sequence)
}
}

// ReceiveChunk processes a received chunk
func (t *Transfer) ReceiveChunk(data []byte) error {
if len(data) < headerSize {
return fmt.Errorf("invalid chunk: too short")
}

// Extract sequence number and size from header
sequence := int(binary.BigEndian.Uint32(data[0:4]))
size := binary.BigEndian.Uint32(data[4:8])

t.debugLog.Printf("Received chunk %d/%d (%d bytes)",
sequence+1, t.info.Chunks, size)

if sequence >= t.info.Chunks {
return fmt.Errorf("invalid chunk sequence: %d", sequence)
}

// Write chunk data to file
offset := int64(sequence) * int64(t.chunkSize)
if _, err := t.file.WriteAt(data[headerSize:headerSize+size], offset); err != nil {
return fmt.Errorf("failed to write chunk %d: %v", sequence, err)
}

// Mark chunk as received
t.mu.Lock()
delete(t.missing, sequence)
t.mu.Unlock()

// Acknowledge receipt
select {
case t.ackChan <- sequence:
case <-time.After(t.timeout):
t.debugLog.Printf("Failed to send acknowledgement for chunk %d", sequence)
}

return nil
}

// RequestMissingChunks requests retransmission of missing chunks
func (t *Transfer) RequestMissingChunks() []int {
t.mu.RLock()
defer t.mu.RUnlock()

var missing []int
for seq := range t.missing {
missing = append(missing, seq)
}

if len(missing) > 0 {
t.debugLog.Printf("Requesting missing chunks: %v", missing)
select {
case t.retransmitCh <- missing:
case <-time.After(t.timeout):
t.debugLog.Printf("Failed to request missing chunks")
}
}

return missing
}

func (t *Transfer) processAcknowledgements() {
for seq := range t.ackChan {
t.mu.Lock()
delete(t.missing, seq)
t.mu.Unlock()
t.debugLog.Printf("Acknowledged chunk %d", seq)
}
}

func (t *Transfer) handleRetransmissions() {
for missing := range t.retransmitCh {
for _, seq := range missing {
if err := t.SendChunk(seq); err != nil {
t.debugLog.Printf("Failed to retransmit chunk %d: %v", seq, err)
}
}
}
}

// Close closes the transfer
func (t *Transfer) Close() error {
if t.file != nil {
return t.file.Close()
}
return nil
}

// Verify verifies the received file's integrity
func (t *Transfer) Verify() error {
// Reset to start of file
if _, err := t.file.Seek(0, 0); err != nil {
return fmt.Errorf("failed to seek file: %v", err)
}

// Calculate MD5 hash
hash := md5.New()
if _, err := io.Copy(hash, t.file); err != nil {
return fmt.Errorf("failed to calculate MD5: %v", err)
}

calculated := fmt.Sprintf("%x", hash.Sum(nil))
if calculated != t.info.MD5 {
return fmt.Errorf("MD5 mismatch: expected %s, got %s", t.info.MD5, calculated)
}

t.debugLog.Printf("File integrity verified: %s", calculated)
return nil
}
