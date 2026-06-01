package session

import (
	"io"
)

// inputWriter forwards pre-marshalled JSON lines from inputCh into the
// session's stdin pipe. Each message must already include its trailing
// newline; inputWriter is intentionally dumb so the caller controls
// framing of the stream-json protocol.
//
// The goroutine exits when inputCh is closed. The stdin pipe is closed
// on exit so Claude CLI sees EOF and can shut down cleanly.
func inputWriter(stdin io.WriteCloser, inputCh <-chan []byte) {
	defer func() { _ = stdin.Close() }()
	for msg := range inputCh {
		if len(msg) == 0 {
			continue
		}
		if _, err := stdin.Write(msg); err != nil {
			// stdin is broken; drain remaining messages without writing
			// so the producer doesn't block on the channel send.
			for range inputCh {
			}
			return
		}
	}
}
