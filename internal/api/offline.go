package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/federbenjamin/desk/internal/model"
)

// snapshot is the last whole tasks.list this machine saw.
type snapshot struct {
	TS    time.Time    `json:"ts"`
	Tasks []model.Task `json:"tasks"`
}

func (c *Client) writeSnapshot(tasks []model.Task) error {
	b, err := json.Marshal(snapshot{TS: time.Now().UTC(), Tasks: tasks})
	if err != nil {
		return err
	}
	path := c.o.Paths.Snapshot()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (c *Client) readSnapshot() (snapshot, error) {
	var s snapshot
	b, err := os.ReadFile(c.o.Paths.Snapshot())
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// openOutbox opens the outbox and takes its lock. The file is locked in place and never renamed, so an
// enqueue and a flush in two processes never lose each other's lines.
func (c *Client) openOutbox(flag int) (*os.File, error) {
	path := c.o.Paths.Outbox()
	if flag&os.O_CREATE != 0 {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// enqueue appends r to the outbox, stamped with the time it was written so a replay keeps it.
func (c *Client) enqueue(r AppendRequest) error {
	if r.Actor.TS == nil {
		now := time.Now().UTC()
		r.Actor.TS = &now
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := c.openOutbox(os.O_WRONLY | os.O_APPEND | os.O_CREATE)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Flush forwards the outbox in order and returns how many entries it sent. It stops at the first failure.
func (c *Client) Flush(ctx context.Context) (int, error) {
	f, err := c.openOutbox(os.O_RDWR)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return 0, err
	}
	var lines [][]byte
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxBody)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) > 0 {
			lines = append(lines, bytes.Clone(sc.Bytes()))
		}
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	sent := 0
	var sendErr error
	for _, line := range lines {
		var r AppendRequest
		if err := json.Unmarshal(line, &r); err != nil {
			sendErr = err
			break
		}
		if err := c.send(ctx, MethodEventsAppend, r, nil, true); err != nil {
			sendErr = err
			break
		}
		sent++
	}
	if sent == 0 {
		return 0, sendErr
	}
	rest := bytes.Join(lines[sent:], []byte{'\n'})
	if len(rest) > 0 {
		rest = append(rest, '\n')
	}
	if err := f.Truncate(0); err != nil {
		return sent, errors.Join(sendErr, err)
	}
	if _, err := f.WriteAt(rest, 0); err != nil {
		return sent, errors.Join(sendErr, err)
	}
	return sent, sendErr
}
