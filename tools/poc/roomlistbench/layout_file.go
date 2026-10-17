package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type savedUser struct {
	Name  string   `json:"name"`
	Size  int      `json:"size"`
	Rooms []string `json:"rooms"`
}

func saveLayout(path string, users []*benchUser) error {
	out := make([]savedUser, len(users))
	for i, u := range users {
		out[i] = savedUser{Name: u.name, Size: u.size, Rooms: u.rooms}
	}
	b, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("layout dir: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write layout: %w", err)
	}
	fmt.Printf("seed: layout of %d users saved to %s\n", len(users), path)
	return nil
}

func loadLayout(path string) ([]*benchUser, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	var in []savedUser
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("decode layout %s: %w", path, err)
	}
	out := make([]*benchUser, len(in))
	for i, u := range in {
		out[i] = &benchUser{name: u.Name, size: u.Size, rooms: u.Rooms}
	}
	return out, nil
}
