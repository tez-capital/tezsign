package logging

import (
	"os"
	"sync"
)

type SimpleLogResetWriter struct {
	mu       sync.Mutex
	FilePath string
	MaxSize  int
	written  int
	File     *os.File
}

func NewSimpleLogResetWriter(filePath string, maxSize int) (*SimpleLogResetWriter, error) {
	writer := &SimpleLogResetWriter{
		FilePath: filePath,
		MaxSize:  maxSize,
	}

	if err := writer.openFile(); err != nil {
		return nil, err
	}

	return writer, nil
}

func (w *SimpleLogResetWriter) openFile() error {
	var err error
	w.File, err = os.OpenFile(w.FilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	return err
}

func (w *SimpleLogResetWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.resetIfNeeded()
	w.written += len(p)

	return w.File.Write(p)
}

func (w *SimpleLogResetWriter) resetIfNeeded() {
	if w.written >= w.MaxSize {
		w.written = 0
		w.File.Close()
		w.openFile()
	}
}

func (w *SimpleLogResetWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.File != nil {
		return w.File.Close()
	}
	return nil
}
