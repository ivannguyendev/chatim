package roomset

import (
	"bufio"
	"errors"
	"fmt"
	"os"
)

type Room struct {
	ID      uint64
	PerRoom uint64
}

type Job struct {
	Rooms    []uint64
	From, To uint64
}

var ErrOrder = errors.New("roomset: order must be room or interleaved")

const burst = 10

func Jobs(order string, ids []uint64, perRoom, batch int) ([]Job, error) {
	if len(ids) == 0 || perRoom <= 0 || batch <= 0 {
		return nil, errors.New("roomset: rooms, per-room and batch must be positive")
	}
	switch order {
	case "room":
		return byRoom(ids, uint64(perRoom), uint64(batch)), nil
	case "interleaved":
		return interleaved(ids, uint64(perRoom), batch), nil
	}
	return nil, fmt.Errorf("%w: %q", ErrOrder, order)
}

func byRoom(ids []uint64, perRoom, batch uint64) []Job {
	var jobs []Job
	for _, id := range ids {
		for from := uint64(1); from <= perRoom; from += batch {
			jobs = append(jobs, Job{Rooms: []uint64{id}, From: from, To: min(from+batch-1, perRoom)})
		}
	}
	return jobs
}

func interleaved(ids []uint64, perRoom uint64, batch int) []Job {
	window := min(uint64(burst), perRoom, uint64(batch))
	roomsPerJob := max(batch/int(window), 1)
	var jobs []Job
	for from := uint64(1); from <= perRoom; from += window {
		to := min(from+window-1, perRoom)
		for i := 0; i < len(ids); i += roomsPerJob {
			jobs = append(jobs, Job{Rooms: ids[i:min(i+roomsPerJob, len(ids))], From: from, To: to})
		}
	}
	return jobs
}

func Write(path string, ids []uint64, perRoom int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, id := range ids {
		fmt.Fprintf(w, "%d %d\n", id, perRoom)
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func Read(path string) ([]Room, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open rooms file (run seed first): %w", err)
	}
	defer f.Close()
	var rooms []Room
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Room
		if _, err := fmt.Sscan(sc.Text(), &r.ID, &r.PerRoom); err == nil && r.PerRoom > 0 {
			rooms = append(rooms, r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(rooms) == 0 {
		return nil, errors.New("rooms file has no rooms")
	}
	return rooms, nil
}
