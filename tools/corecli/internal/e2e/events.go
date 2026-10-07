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
	Kind    string `json:"kind,omitempty"`
	Room    string `json:"room"`
	ID      string `json:"id"`
	Seq     uint64 `json:"seq"`
	CID     string `json:"cid"`
	User    string `json:"user,omitempty"`
	Version uint32 `json:"version,omitempty"`
	Text    string `json:"text,omitempty"`
	Subject string `json:"subject,omitempty"`
}

func (e Event) IsChange() bool { return e.Kind == KindEdited || e.Kind == KindDeleted }

func (e Event) IsCreated() bool { return e.Kind == "" || e.Kind == KindCreated }

func EventOf(subject string, ev *chatimv1.Event) (Event, bool) {
	if created := ev.GetMessageCreated(); created != nil {
		return Event{Kind: KindCreated, Room: ev.GetRoomId(), ID: ev.GetId(), Seq: ev.GetSeq(), CID: created.GetMessage().GetCid(), Subject: subject}, true
	}
	if edited := ev.GetMessageEdited(); edited != nil {
		return changeOf(subject, ev, KindEdited, edited.GetMessage(), edited.GetVer()), true
	}
	if deleted := ev.GetMessageDeleted(); deleted != nil {
		return changeOf(subject, ev, KindDeleted, deleted.GetMessage(), deleted.GetVer()), true
	}
	return markOf(subject, ev)
}

func changeOf(subject string, ev *chatimv1.Event, kind string, m *chatimv1.Message, version uint32) Event {
	return Event{Kind: kind, Room: m.GetRoomId(), ID: ev.GetId(), Seq: m.GetSeq(), CID: m.GetCid(), Version: version, Text: m.GetText(), Subject: subject}
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
