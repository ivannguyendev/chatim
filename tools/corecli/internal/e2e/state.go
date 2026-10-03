package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Ack struct {
	CID string `json:"cid"`
	Seq uint64 `json:"seq"`
	Pts uint64 `json:"pts"`
}

type State struct {
	Tenant string `json:"tenant"`
	User   string `json:"user"`
	Room   string `json:"room"`
	Owner  string `json:"owner"`
	Acks   []Ack  `json:"acks"`
}

func TextFor(cid string) string { return "e2e message " + cid }

func Load(path string) (State, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return State{}, fmt.Errorf("read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("decode state %s: %w", path, err)
	}
	return s, nil
}

func Save(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}
