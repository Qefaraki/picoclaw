package bus

import (
	"sync"
	"time"
)

// StreamNotifier accumulates text deltas and flushes the full accumulated
// text to a callback at a throttled interval (default 1.5s). This prevents
// excessive Telegram API edits while still showing streaming progress.
type StreamNotifier struct {
	mu       sync.Mutex
	text     string
	onUpdate func(fullText string)
	ticker   *time.Ticker
	done     chan struct{}
	dirty    bool
}

// NewStreamNotifier creates a notifier that calls onUpdate with the full
// accumulated text every interval.
func NewStreamNotifier(interval time.Duration, onUpdate func(fullText string)) *StreamNotifier {
	sn := &StreamNotifier{
		onUpdate: onUpdate,
		ticker:   time.NewTicker(interval),
		done:     make(chan struct{}),
	}

	go sn.loop()
	return sn
}

func (sn *StreamNotifier) loop() {
	for {
		select {
		case <-sn.ticker.C:
			sn.mu.Lock()
			if sn.dirty && sn.text != "" {
				text := sn.text
				sn.dirty = false
				sn.mu.Unlock()
				sn.onUpdate(text)
			} else {
				sn.mu.Unlock()
			}
		case <-sn.done:
			return
		}
	}
}

// Append adds a text delta to the accumulator.
func (sn *StreamNotifier) Append(delta string) {
	sn.mu.Lock()
	sn.text += delta
	sn.dirty = true
	sn.mu.Unlock()
}

// Flush stops the ticker and performs a final push if there's unsent content.
func (sn *StreamNotifier) Flush() {
	sn.ticker.Stop()
	close(sn.done)

	sn.mu.Lock()
	if sn.dirty && sn.text != "" {
		text := sn.text
		sn.dirty = false
		sn.mu.Unlock()
		sn.onUpdate(text)
	} else {
		sn.mu.Unlock()
	}
}

// FullText returns the current accumulated text.
func (sn *StreamNotifier) FullText() string {
	sn.mu.Lock()
	defer sn.mu.Unlock()
	return sn.text
}
