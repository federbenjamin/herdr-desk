package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/federbenjamin/desk/internal/config"
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
	return config.WriteFileAtomic(c.o.Paths.Snapshot(), b)
}

func (c *Client) readSnapshot() (snapshot, error) {
	var s snapshot
	b, err := os.ReadFile(c.o.Paths.Snapshot())
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// lockOutbox takes the outbox's lock, a file beside it. The outbox itself is replaced by a rename on every
// flush, so a lock on the outbox's own inode would let an enqueue append to the file a flush just replaced.
func (c *Client) lockOutbox() (unlock func(), err error) {
	path := c.o.Paths.Outbox() + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
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
	unlock, err := c.lockOutbox()
	if err != nil {
		return err
	}
	defer unlock()
	f, err := os.OpenFile(c.o.Paths.Outbox(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Flush forwards the outbox in order and returns how many entries it sent. It stops when the home cannot be
// reached, or answers in a way a retry may change (scan-failed, a 500), keeping what was not sent. An entry the
// home refuses for a cause in the entry itself (any other refusal, a 400, a 413) will never be accepted: it is
// removed, reported through ClientOptions.Refused, and Flush goes on. A line that does not parse is removed the
// same way, reported with the code bad-input. The entries kept replace the outbox through a rename, so a
// failure leaves the old outbox whole.
func (c *Client) Flush(ctx context.Context) (int, error) {
	unlock, err := c.lockOutbox()
	if err != nil {
		return 0, err
	}
	defer unlock()
	data, err := os.ReadFile(c.o.Paths.Outbox())
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil || len(data) == 0 {
		return 0, err
	}
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	refused := func(kind model.Kind, r *model.Refusal) {
		if c.o.Refused != nil {
			c.o.Refused(kind, r)
		}
	}
	sent, done := 0, 0
	var sendErr error
	for _, line := range lines {
		var r AppendRequest
		if err := json.Unmarshal(line, &r); err != nil {
			refused(r.Kind, &model.Refusal{Code: model.CodeBadInput, Msg: "an outbox line does not parse: " + err.Error()})
			done++
			continue
		}
		err := c.send(ctx, MethodEventsAppend, r, nil, true)
		if ref := refusedForGood(err); ref != nil {
			refused(r.Kind, ref)
			done++
			continue
		}
		if err != nil {
			sendErr = err
			break
		}
		sent++
		done++
	}
	if done == 0 {
		return 0, sendErr
	}
	rest := bytes.Join(lines[done:], []byte{'\n'})
	if len(rest) > 0 {
		rest = append(rest, '\n')
	}
	return sent, errors.Join(sendErr, config.WriteFileAtomic(c.o.Paths.Outbox(), rest))
}

// refusedForGood returns the refusal of an entry the home will never accept, nil for an answer a retry may
// change.
func refusedForGood(err error) *model.Refusal {
	if ref, ok := model.AsRefusal(err); ok {
		if ref.Code == model.CodeHomeUnreachable || ref.Code == model.CodeScanFailed {
			return nil
		}
		return ref
	}
	var he *httpError
	if errors.As(err, &he) && (he.status == http.StatusBadRequest || he.status == http.StatusRequestEntityTooLarge) {
		return &model.Refusal{Code: model.CodeBadInput, Msg: he.msg}
	}
	return nil
}
