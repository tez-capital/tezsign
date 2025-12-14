package logging

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ----------------- Config -----------------

type Config struct {
	Level        slog.Level // default: Info
	Format       string     // "text" or "json" (default "text")
	File         string     // path to log file; empty = no file
	AlsoStderr   bool       // default true
	MaxSizeMB    int        // default 50
	SetAsDefault bool       // set slog.SetDefault
}

const (
	// LevelAll is a custom level logging all errors.
	LevelAll slog.Level = -100
	// LevelFatal is a custom level above Error used for fatal events.
	LevelFatal slog.Level = 20
)

func DefaultConfig() Config {
	return Config{
		Level:      slog.LevelInfo,
		Format:     "text",
		AlsoStderr: true,
		MaxSizeMB:  50,
	}
}

// NewConfigFromEnv reads {PREFIX}_LOG* variables; falls back to BROKER_LOG* if prefix empty.
func NewConfigFromEnv() Config {
	cfg := DefaultConfig()

	// Level
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "all":
		cfg.Level = LevelAll
	case "debug":
		cfg.Level = slog.LevelDebug
	case "warn", "warning":
		cfg.Level = slog.LevelWarn
	case "error":
		cfg.Level = slog.LevelError
	case "fatal":
		cfg.Level = LevelFatal
	}

	// Format
	switch strings.ToLower(os.Getenv("LOG_FORMAT")) {
	case "json":
		cfg.Format = "json"
	case "text", "":
		cfg.Format = "text"
	}

	cfg.File = strings.TrimSpace(os.Getenv("LOG_FILE"))
	cfg.AlsoStderr = envBool(os.Getenv("LOG_STDERR"), true)
	cfg.MaxSizeMB = envInt(os.Getenv("LOG_MAX_SIZE_MB"), 5)

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

// New builds a slog.Logger using cfg; returns the logger and the truncating simple log writer.
func New(cfg Config) (*slog.Logger, io.Writer) {
	handlers := make([]slog.Handler, 0, 2)

	var logWriter io.Writer
	if cfg.File != "" {
		lw, err := NewSimpleLogResetWriter(cfg.File, cfg.MaxSizeMB*1024*1024)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logging: file %q disabled: %v\n", cfg.File, err)
		} else {
			logWriter = lw
			setCurrentFile(cfg.File)
			switch cfg.Format {
			case "json":
				handlers = append(handlers, slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: cfg.Level}))
			default: // text
				handlers = append(handlers, slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: cfg.Level}))
			}
		}
	}

	// Fatal-only handler that appends daily fatal logs next to the main log.
	if cfg.File != "" {
		handlers = append(handlers, newFatalFileHandler(cfg.File, cfg.Format))
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

// All logs a message at LevelAll using a background context.
func All(l *slog.Logger, msg string, args ...any) {
	if l == nil {
		return
	}
	l.Log(context.Background(), LevelAll, msg, args...)
}

// Fatal logs a message at LevelFatal using a background context.
func Fatal(l *slog.Logger, msg string, args ...any) {
	if l == nil {
		return
	}
	l.Log(context.Background(), LevelFatal, msg, args...)
}

// fatalFileHandler appends fatal records to daily files next to the main log.
type fatalFileHandler struct {
	basePath string
	format   string
	attrs    []slog.Attr
	groups   []string
}

func newFatalFileHandler(basePath, format string) slog.Handler {
	return fatalFileHandler{basePath: basePath, format: format}
}

func (h fatalFileHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= LevelFatal
}

func (h fatalFileHandler) Handle(ctx context.Context, r slog.Record) error {
	if !h.Enabled(ctx, r.Level) {
		return nil
	}

	path := fatalFilePath(h.basePath, time.Now())
	if err := EnsureDir(path); err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o660)
	if err != nil && errors.Is(err, os.ErrPermission) {
		if rmErr := os.Remove(path); rmErr == nil {
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o660)
		}
	}
	if err != nil {
		return err
	}
	if f != nil {
		_ = f.Chmod(0o660)
		_ = os.Chmod(path, 0o660)
	}
	defer f.Close()

	var handler slog.Handler
	switch h.format {
	case "json":
		handler = slog.NewJSONHandler(f, &slog.HandlerOptions{Level: LevelFatal})
	default:
		handler = slog.NewTextHandler(f, &slog.HandlerOptions{Level: LevelFatal})
	}

	for _, g := range h.groups {
		handler = handler.WithGroup(g)
	}
	if len(h.attrs) > 0 {
		handler = handler.WithAttrs(h.attrs)
	}
	return handler.Handle(ctx, r)
}

func (h fatalFileHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.attrs = append(h.attrs, attrs...)
	return h
}

func (h fatalFileHandler) WithGroup(name string) slog.Handler {
	h.groups = append(h.groups, name)
	return h
}

func fatalFilePath(base string, now time.Time) string {
	dir := filepath.Dir(base)
	if dir == "" || dir == "." {
		dir = "."
	}
	name := filepath.Base(base)
	ext := filepath.Ext(name)
	name = strings.TrimSuffix(name, ext)
	if name == "" {
		name = "fatal"
	}
	return filepath.Join(dir, fmt.Sprintf("%s.error.%s", name, now.Format("2006-01-02")))
}

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
