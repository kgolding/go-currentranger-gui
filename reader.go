package main

import (
	"bufio"
	"fmt"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	baudRate      = 230400
	readTimeout   = 50 * time.Millisecond
	maxPoints     = 500000 // rolling buffer (~6 min at 1300 Hz)
)

// SerialReader mirrors the Python SerialReader thread: it owns the serial
// port, runs its read loop on a dedicated goroutine, and exposes a
// mutex-protected snapshot of the accumulated (timestamp, current) samples.
type SerialReader struct {
	portName string
	port     serial.Port

	mu         sync.Mutex
	timestamps []float64
	currents   []float64
	running    bool
	err        error

	stopCh chan struct{}
	done   chan struct{} // closed once run() has fully exited and released the port
	once   sync.Once
}

func NewSerialReader(portName string) *SerialReader {
	return &SerialReader{
		portName: portName,
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// connect opens the port and reproduces the Python "toggle USB logging"
// handshake: send 'u', see if data starts flowing, and if not, assume
// logging was already on and toggle it again.
func (r *SerialReader) connect() error {
	mode := &serial.Mode{BaudRate: baudRate}
	p, err := serial.Open(r.portName, mode)
	if err != nil {
		return fmt.Errorf("open %s: %w", r.portName, err)
	}
	r.port = p

	// Any failure past this point must close the port before returning,
	// otherwise the OS-level handle leaks and the next connect attempt on
	// this port fails with a spurious "busy" error even though nothing is
	// really holding it anymore.
	closeOnErr := func(err error) error {
		if err != nil {
			_ = p.Close()
		}
		return err
	}

	time.Sleep(200 * time.Millisecond)
	_ = p.ResetInputBuffer()

	if err := p.SetReadTimeout(300 * time.Millisecond); err != nil {
		return closeOnErr(err)
	}
	if _, err := p.Write([]byte("u")); err != nil {
		return closeOnErr(err)
	}
	time.Sleep(300 * time.Millisecond)

	peek := make([]byte, 1)
	_ = p.SetReadTimeout(10 * time.Millisecond)
	n, _ := p.Read(peek)
	if n == 0 {
		// No data flowing - logging was ON and we just turned it OFF.
		// Toggle again to turn it back ON.
		if _, err := p.Write([]byte("u")); err != nil {
			return closeOnErr(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = p.ResetInputBuffer()
	return closeOnErr(p.SetReadTimeout(readTimeout))
}

// Start launches the read loop in the background. Errors from connecting
// surface via Error() shortly after; poll IsRunning()/Error() from the UI.
func (r *SerialReader) Start() {
	r.mu.Lock()
	r.running = true
	r.mu.Unlock()
	go r.run()
}

func (r *SerialReader) run() {
	// Stop() blocks on r.done so it can't return until the port is truly
	// released — closing r.port here (not in Stop, which runs on a
	// different goroutine than this read loop) avoids racing an in-flight
	// blocking Read against a concurrent Close, which on some serial
	// drivers leaves the port lingering "busy" for a moment after Close
	// returns rather than releasing it immediately.
	defer close(r.done)

	if err := r.connect(); err != nil {
		r.mu.Lock()
		r.err = err
		r.running = false
		r.mu.Unlock()
		return
	}
	defer func() { _ = r.port.Close() }()

	reader := bufio.NewReader(r.port)
	for {
		select {
		case <-r.stopCh:
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if line == "" {
			if err != nil {
				// Real I/O errors (not plain read timeouts) end the loop.
				if !isTimeoutErr(err) {
					r.mu.Lock()
					r.err = err
					r.running = false
					r.mu.Unlock()
					return
				}
			}
			continue
		}

		value, ok := parseCurrentRangerLine(line)
		if !ok {
			continue
		}
		now := float64(time.Now().UnixNano()) / 1e9

		r.mu.Lock()
		r.timestamps = append(r.timestamps, now)
		r.currents = append(r.currents, value)
		if len(r.timestamps) > maxPoints {
			overflow := len(r.timestamps) - maxPoints
			r.timestamps = r.timestamps[overflow:]
			r.currents = r.currents[overflow:]
		}
		r.mu.Unlock()
	}
}

// isTimeoutErr treats a zero-byte, nil-error read (the common timeout
// behavior for go.bug.st/serial) or a bufio timeout-flavored error as
// non-fatal so the loop keeps polling instead of tearing down the port.
func isTimeoutErr(err error) bool {
	return err != nil && err.Error() == "EOF"
}

func (r *SerialReader) Stop() {
	r.once.Do(func() { close(r.stopCh) })
	<-r.done // wait for run() to release the port before returning
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
}

// Snapshot returns copies of the current timestamp/current buffers so the
// UI thread never touches the live slices directly.
func (r *SerialReader) Snapshot() ([]float64, []float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ts := make([]float64, len(r.timestamps))
	copy(ts, r.timestamps)
	cur := make([]float64, len(r.currents))
	copy(cur, r.currents)
	return ts, cur
}

func (r *SerialReader) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.timestamps = nil
	r.currents = nil
}

func (r *SerialReader) Error() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *SerialReader) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (r *SerialReader) Port() string {
	return r.portName
}

// listPorts enumerates available serial ports (mirrors
// serial.tools.list_ports.comports()).
func listPorts() []string {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil
	}
	return ports
}
