package logging

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ----------------- Config -----------------

type Config struct {
	Level        slog.Level // default: Info
	Format       string     // "text" or "json" (default "text")
	File         string     // path to log file; empty = no file
	AlsoStderr   bool       // default true
	NoColor      bool       // pretty text: no colors
	MaxSizeMB    int        // default 50
	MaxBackups   int        // default 3
	MaxAgeDays   int        // default 14
	Compress     bool       // default true
	SetAsDefault bool       // set slog.SetDefault
}

func DefaultConfig() Config {
	return Config{
		Level:      slog.LevelInfo,
		Format:     "text",
		AlsoStderr: true,
		MaxSizeMB:  50, MaxBackups: 3, MaxAgeDays: 14,
		Compress: true,
	}
}

// NewConfigFromEnv reads {PREFIX}_LOG* variables; falls back to BROKER_LOG* if prefix empty.
func NewConfigFromEnv() Config {
	cfg := DefaultConfig()

	// Level
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "all":
		cfg.Level = slog.Level(-100)
	case "debug":
		cfg.Level = slog.LevelDebug
	case "warn", "warning":
		cfg.Level = slog.LevelWarn
	case "error":
		cfg.Level = slog.LevelError
	}

	// Format
	switch strings.ToLower(os.Getenv("LOG_FORMAT")) {
	case "json":
		cfg.Format = "json"
	case "text", "":
		cfg.Format = "text"
	}

	// File + rotation
	cfg.File = strings.TrimSpace(os.Getenv("LOG_FILE"))
	cfg.AlsoStderr = envBool(os.Getenv("LOG_STDERR"), true)
	cfg.NoColor = envBool(os.Getenv("LOG_NO_COLOR"), false)
	cfg.MaxSizeMB = envInt(os.Getenv("LOG_MAX_SIZE_MB"), 5)
	cfg.MaxBackups = envInt(os.Getenv("LOG_MAX_BACKUPS"), 0)
	cfg.MaxAgeDays = envInt(os.Getenv("LOG_MAX_AGE_DAYS"), 14)
	cfg.Compress = envBool(os.Getenv("LOG_COMPRESS"), false)

	cfg.SetAsDefault = true
	return cfg
}

func envBool(s string, def bool) bool {
	if s == "" {
		return def
	}
	switch strings.ToLower(s) {
	case "1", "true", "t", "yes", "y":
		return true
	case "0", "false", "f", "no", "n":
		return false
	default:
		return def
	}
}
func envInt(s string, def int) int {
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// ----------------- Setup -----------------

// globals for Logs RPC to know the file to tail
var (
	curFilePath string
	curFileMu   sync.RWMutex
)

func CurrentFile() string {
	curFileMu.RLock()
	defer curFileMu.RUnlock()
	return curFilePath
}
func setCurrentFile(p string) {
	curFileMu.Lock()
	curFilePath = p
	curFileMu.Unlock()
}

// MultiHandler fans out to multiple slog.Handlers
type MultiHandler struct{ hs []slog.Handler }

func (m MultiHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range m.hs {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}
func (m MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range m.hs {
		if err := h.Handle(ctx, r); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
func (m MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithAttrs(attrs)
	}
	return MultiHandler{hs: out}
}
func (m MultiHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		out[i] = h.WithGroup(name)
	}
	return MultiHandler{hs: out}
}

// DefaultFileInExecDir returns <exec-dir>/<name>, best-effort.
// If exec dir can't be resolved, falls back to "./<name>".
func DefaultFileInExecDir(name string) string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "./" + name
	}
	return filepath.Join(filepath.Dir(exe), name)
}

// EnsureDir creates the parent directory of path if needed.
func EnsureDir(path string) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

// New builds a slog.Logger using cfg; returns the logger and the (optional) rotating writer.
func New(cfg Config) (*slog.Logger, io.Writer) {
	handlers := make([]slog.Handler, 0, 2)

	var logWriter io.Writer
	if cfg.File != "" {
		logWriter, _ = NewSimpleLogResetWriter(cfg.File, cfg.MaxSizeMB*1024*1024)
		setCurrentFile(cfg.File)
		switch cfg.Format {
		case "json":
			handlers = append(handlers, slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: cfg.Level}))
		default: // text
			handlers = append(handlers, slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: cfg.Level}))
		}
	}

	// stderr handler
	if cfg.AlsoStderr {
		switch cfg.Format {
		case "json":
			handlers = append(handlers, slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.Level}))
		default:
			handlers = append(handlers, slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.Level}))
		}
	}

	var h slog.Handler
	if len(handlers) == 0 {
		// fallback to stderr text
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.Level})
	} else if len(handlers) == 1 {
		h = handlers[0]
	} else {
		h = MultiHandler{hs: handlers}
	}

	l := slog.New(h)
	if cfg.SetAsDefault {
		slog.SetDefault(l)
	}
	return l, logWriter
}

func NewFromEnv() (*slog.Logger, io.Writer) {
	return New(NewConfigFromEnv())
}

// ----------------- Tail utility -----------------

// TailLastLines reads up to n last newline-delimited lines from a file.
// Works with rotated file as long as you pass the current path.
func TailLastLines(path string, n int) ([]string, error) {
	if n <= 0 {
		n = 100
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Fast path: read from end in blocks; for simplicity we can just scan forward if file is small.
	// Robust implementation: seek from end, scan backwards.
	const block = 64 * 1024
	stat, _ := f.Stat()
	size := stat.Size()
	var (
		pos    = size
		buf    []byte
		chunks [][]byte
		lines  []string
	)
	for pos > 0 && len(lines) <= n {
		read := int64(block)
		if pos < read {
			read = pos
		}
		pos -= read
		tmp := make([]byte, read)
		if _, err := f.ReadAt(tmp, pos); err != nil {
			return nil, err
		}
		chunks = append(chunks, tmp)
	}
	// stitch chunks in reverse into buf
	for i := len(chunks) - 1; i >= 0; i-- {
		buf = append(buf, chunks[i]...)
	}
	sc := bufio.NewScanner(strings.NewReader(string(buf)))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
