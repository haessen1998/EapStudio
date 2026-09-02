package sink

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"eapstudio/internal/event"
)

// FileSink appends one canonical event per line as durable JSONL. Each write is
// flushed by closing the file, which favors auditability over bulk throughput.
type FileSink struct {
	name string
	path string
	mu   sync.Mutex
}

func NewFile(name, path string) (*FileSink, error) {
	if name == "" || path == "" {
		return nil, fmt.Errorf("file sink name and path are required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return &FileSink{name: name, path: path}, nil
}

func (s *FileSink) Name() string { return s.name }
func (s *FileSink) Path() string { return s.path }

func (s *FileSink) Send(ctx context.Context, value event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
