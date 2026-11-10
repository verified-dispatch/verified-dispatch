package procs

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func capped(c *CappedWriter) bool {
	select {
	case <-c.Capped():
		return true
	default:
		return false
	}
}

func TestCappedWriterStoresUpToTheCapAndKeepsCounting(t *testing.T) {
	var sink bytes.Buffer
	c := NewCappedWriter(&sink, 1000)
	chunk := bytes.Repeat([]byte("x"), 400)
	for i := 0; i < 3; i++ {
		n, err := c.Write(chunk)
		if n != 400 || err != nil {
			t.Fatalf("write %d: n=%d err=%v, want 400, nil", i, n, err)
		}
	}
	if sink.Len() != 1000 || c.Stored() != 1000 {
		t.Fatalf("stored %d bytes in sink, %d by count; want 1000", sink.Len(), c.Stored())
	}
	if c.Seen() != 1200 {
		t.Fatalf("seen = %d, want 1200", c.Seen())
	}
	if !capped(c) {
		t.Fatal("Capped not closed after the cap was exceeded")
	}
	// Writes after the cap are counted, not stored, and still succeed.
	if n, err := c.Write(chunk); n != 400 || err != nil || sink.Len() != 1000 || c.Seen() != 1600 {
		t.Fatalf("after cap: n=%d err=%v sink=%d seen=%d", n, err, sink.Len(), c.Seen())
	}
}

func TestCappedWriterExactlyTheCapIsNotCapped(t *testing.T) {
	var sink bytes.Buffer
	c := NewCappedWriter(&sink, 10)
	if _, err := c.Write(bytes.Repeat([]byte("y"), 10)); err != nil {
		t.Fatal(err)
	}
	if capped(c) {
		t.Fatal("a stream of exactly the cap must not count as capped")
	}
	if _, err := c.Write([]byte("z")); err != nil {
		t.Fatal(err)
	}
	if !capped(c) || sink.String() != "yyyyyyyyyy" {
		t.Fatalf("capped=%v sink=%q", capped(c), sink.String())
	}
}

type failingWriter struct{ n int }

func (f *failingWriter) Write(p []byte) (int, error) {
	f.n++
	return 0, errors.New("disk full")
}

func TestCappedWriterRecordsASinkFailureAndKeepsDraining(t *testing.T) {
	f := &failingWriter{}
	c := NewCappedWriter(f, 1000)
	for i := 0; i < 3; i++ {
		if n, err := c.Write([]byte("abc")); n != 3 || err != nil {
			t.Fatalf("write %d: n=%d err=%v; the pipe must keep draining", i, n, err)
		}
	}
	if c.Err() == nil || c.Err().Error() != "disk full" {
		t.Fatalf("Err = %v, want the sink's error", c.Err())
	}
	if f.n != 1 {
		t.Fatalf("sink called %d times, want 1 (stop storing after the first failure)", f.n)
	}
	if c.Seen() != 9 || c.Stored() != 0 {
		t.Fatalf("seen=%d stored=%d", c.Seen(), c.Stored())
	}
}

func TestCappedWriterIsSafeForConcurrentWriters(t *testing.T) {
	var sink bytes.Buffer
	c := NewCappedWriter(&sink, 5000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				_, _ = c.Write([]byte("0123456789"))
			}
		}()
	}
	wg.Wait()
	if sink.Len() != 5000 || c.Stored() != 5000 || c.Seen() != 80000 || !capped(c) {
		t.Fatalf("sink=%d stored=%d seen=%d capped=%v", sink.Len(), c.Stored(), c.Seen(), capped(c))
	}
}
