// Package procs starts the builder, prepare, and gate commands and keeps
// them contained. Each child runs in its own session and process group with
// a fixed stdin, environment, and working directory. On Linux the tool is a
// child subreaper and the child dies with the tool. The descendants of a
// child can be listed and torn down within a bound, and captured output is
// capped without ever blocking the child. Platform differences live in the
// _linux and _darwin files behind one set of functions.
package procs

import (
	"io"
	"sync"
)

// CappedWriter stores what it is given up to a byte cap, then keeps
// accepting and discarding, so the process writing to it never blocks on a
// full pipe before it is torn down. Capped is closed the moment a byte
// beyond the cap arrives, so the caller can end the run at once.
type CappedWriter struct {
	w      io.Writer
	cap    int64
	mu     sync.Mutex
	seen   int64
	stored int64
	err    error
	capped chan struct{}
	once   sync.Once
}

// NewCappedWriter stores at most cap bytes in w.
func NewCappedWriter(w io.Writer, cap int64) *CappedWriter {
	return &CappedWriter{w: w, cap: cap, capped: make(chan struct{})}
}

// Write counts every byte, stores those under the cap, and always reports
// the full length with no error. A failure of the underlying writer is kept
// in Err and ends storing; it does not stop the stream, for the same reason
// the cap does not.
func (c *CappedWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen += int64(len(p))
	if c.seen > c.cap {
		c.once.Do(func() { close(c.capped) })
	}
	if c.err != nil {
		return len(p), nil
	}
	room := c.cap - c.stored
	if room <= 0 {
		return len(p), nil
	}
	n := int64(len(p))
	if n > room {
		n = room
	}
	written, err := c.w.Write(p[:n])
	c.stored += int64(written)
	if err != nil {
		c.err = err
	}
	return len(p), nil
}

// Capped is closed when a byte beyond the cap has arrived.
func (c *CappedWriter) Capped() <-chan struct{} { return c.capped }

// Seen is the number of bytes received, stored or not.
func (c *CappedWriter) Seen() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen
}

// Stored is the number of bytes written to the underlying writer.
func (c *CappedWriter) Stored() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stored
}

// Err is the first error the underlying writer returned, or nil.
func (c *CappedWriter) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
