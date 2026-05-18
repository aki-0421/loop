package runstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type Event map[string]any

type EventLog struct {
	Path string
	Now  func() time.Time
}

func AppendEvent(path string, event Event) error {
	return EventLog{Path: path}.Append(event)
}

func (l EventLog) Append(event Event) error {
	if l.Path == "" {
		return errors.New("runstate: empty event log path")
	}
	if event == nil {
		return errors.New("runstate: nil event")
	}
	if _, ok := event["type"]; !ok {
		return errors.New("runstate: event missing type")
	}
	if _, ok := event["ts"]; !ok {
		now := time.Now
		if l.Now != nil {
			now = l.Now
		}
		event["ts"] = now().UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
