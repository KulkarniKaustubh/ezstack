package ui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// SpinnerDelay is the delay before showing a spinner (only show for slow operations)
const SpinnerDelay = 1500 * time.Millisecond

// Spinner represents a simple loading spinner
type Spinner struct {
	message  string
	stop     chan bool
	wg       sync.WaitGroup
	started  bool
	mu       sync.Mutex
	stopOnce sync.Once
}

// NewSpinner creates a new spinner with the given message
func NewSpinner(message string) *Spinner {
	return &Spinner{
		message: message,
		stop:    make(chan bool),
	}
}

// Start begins the spinner animation
func (s *Spinner) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		i := 0
		for {
			select {
			case <-s.stop:
				// Clear the spinner line if it was shown
				s.mu.Lock()
				if s.started {
					fmt.Fprintf(os.Stderr, "\r\033[K")
				}
				s.mu.Unlock()
				return
			default:
				s.mu.Lock()
				s.started = true
				s.mu.Unlock()
				fmt.Fprintf(os.Stderr, "\r%s%s%s %s", Cyan, frames[i], Reset, s.message)
				i = (i + 1) % len(frames)
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

// Stop stops the spinner
func (s *Spinner) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
	})
	s.wg.Wait()
}

// DelayedSpinner represents a spinner that only shows after a delay
type DelayedSpinner struct {
	message string
	delay   time.Duration
	spinner *Spinner
	timer   *time.Timer
	mu      sync.Mutex
	stopped bool
}

// NewDelayedSpinner creates a spinner that only shows after the configured delay
func NewDelayedSpinner(message string) *DelayedSpinner {
	return &DelayedSpinner{
		message: message,
		delay:   SpinnerDelay,
	}
}

// Start begins the delayed spinner (will only show if not stopped before delay)
func (ds *DelayedSpinner) Start() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.timer = time.AfterFunc(ds.delay, func() {
		ds.mu.Lock()
		defer ds.mu.Unlock()
		if !ds.stopped {
			ds.spinner = NewSpinner(ds.message)
			ds.spinner.Start()
		}
	})
}

// Stop stops the delayed spinner
func (ds *DelayedSpinner) Stop() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ds.stopped = true
	if ds.timer != nil {
		ds.timer.Stop()
	}
	if ds.spinner != nil {
		ds.spinner.Stop()
	}
}

// WithSpinner runs a function with a delayed spinner
// The spinner only shows if the function takes longer than SpinnerDelay
func WithSpinner(message string, fn func() error) error {
	spinner := NewDelayedSpinner(message)
	spinner.Start()
	defer spinner.Stop()
	return fn()
}
