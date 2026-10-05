package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Event struct {
	Room    string `json:"room"`
	ID      string `json:"id"`
	Seq     uint64 `json:"seq"`
	CID     string `json:"cid"`
	Subject string `json:"subject,omitempty"`
}

func EventOf(subject string, ev *chatimv1.Event) (Event, bool) {
	created := ev.GetMessageCreated()
	if created == nil {
		return Event{}, false
	}
	return Event{
		Room:    ev.GetRoomId(),
		ID:      ev.GetId(),
		Seq:     ev.GetSeq(),
		CID:     created.GetMessage().GetCid(),
		Subject: subject,
	}, true
}

func ReadEvents(path string) ([]Event, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	if end := bytes.LastIndexByte(data, '\n'); end >= 0 {
		data = data[:end]
	} else {
		data = nil
	}
	var out []Event
	for i, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("events line %d: %w", i+1, err)
		}
		out = append(out, ev)
	}
	return out, nil
}
