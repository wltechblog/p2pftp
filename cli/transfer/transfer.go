package transfer

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"time"
)

// FileInfo contains metadata about a file being transferred
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	MD5  string `json:"md5"`
}

// FileTransfer contains information about a file transfer
type FileTransfer struct {
	*FileInfo
	file     *os.File
	filePath string
}

// ChunkInfo contains information about a chunk being transferred
type ChunkInfo struct {
	Sequence    int
	TotalChunks int
	Size        int
}

// TransferState contains the state of a file transfer
type TransferState struct {
	inProgress           bool
	startTime            time.Time
	lastUpdate           time.Time
	fileTransfer         *FileTransfer
	chunks               [][]byte
	totalChunks          int
	lastReceivedSequence int
	receivedChunks       map[int]bool
	missingChunks        map[int]bool
	receivedSize         int64
	lastUpdateSize       int64
	expectedChunk        *ChunkInfo
	windowSize           int
	nextSequenceToSend   int
	lastAckedSequence    int
	unacknowledgedChunks map[int]bool
	retransmissionQueue  []int
	chunkTimestamps      map[int]time.Time
	congestionWindow     int
	consecutiveTimeouts  int
	confirmHandler       func(int)
}

// ChunkConfirm message for acknowledging received chunks
type ChunkConfirm struct {
	Type     string `json:"type"`
	Sequence int    `json:"sequence"`
}

// CalculateMD5 calculates the MD5 hash of a file
func CalculateMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	hash := md5.New()
	buf := make([]byte, 32768)

	for {
		n, err := file.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// NewTransferState creates a new transfer state
func NewTransferState() *TransferState {
	return &TransferState{
		inProgress:           false,
		receivedChunks:       make(map[int]bool),
		missingChunks:        make(map[int]bool),
		unacknowledgedChunks: make(map[int]bool),
		retransmissionQueue:  make([]int, 0),
		chunkTimestamps:      make(map[int]time.Time),
	}
}

// NewFileTransfer creates a new file transfer
func NewFileTransfer(fileInfo *FileInfo, file *os.File, filePath string) *FileTransfer {
	return &FileTransfer{
		FileInfo: fileInfo,
		file:     file,
		filePath: filePath,
	}
}

// Close closes the file transfer
func (ft *FileTransfer) Close() {
	if ft != nil && ft.file != nil {
		ft.file.Close()
	}
}
