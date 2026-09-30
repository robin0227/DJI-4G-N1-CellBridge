package voice

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cellbridge/cellbridge/gateway/internal/modem"
)

// ALSAAudio connects a USB Audio Class modem through the standard ALSA
// command-line tools. Keeping the process boundary here means the rest of
// Gateway only sees signed PCM and does not need cgo or vendor libraries.
// The configured devices must support mono S16_LE at 8000 Hz.
//
// Locking rules (learned the hard way — a Process.Wait under a.mu wedged
// the whole gateway): a.mu only guards the pointer fields. Killing and
// waiting on child processes happens OUTSIDE the lock, so a stuck child
// can never hold up the next call's Start.
type ALSAAudio struct {
	captureDevice  string
	playbackDevice string

	mu       sync.Mutex
	call     modem.CallID
	capture  *exec.Cmd
	playback *exec.Cmd
	rx       io.ReadCloser
	tx       io.WriteCloser
	txMu     sync.Mutex
	once     sync.Once
}

func OpenALSA(captureDevice, playbackDevice string) (*ALSAAudio, error) {
	captureDevice = strings.TrimSpace(captureDevice)
	playbackDevice = strings.TrimSpace(playbackDevice)
	if captureDevice == "" || playbackDevice == "" {
		return nil, fmt.Errorf("alsa voice backend requires capture and playback devices")
	}
	return &ALSAAudio{captureDevice: captureDevice, playbackDevice: playbackDevice}, nil
}

func (a *ALSAAudio) Probe(ctx context.Context) (modem.AudioCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return modem.AudioCapabilities{}, err
	}
	if _, err := exec.LookPath("arecord"); err != nil {
		return modem.AudioCapabilities{}, fmt.Errorf("arecord is unavailable: %w", err)
	}
	if _, err := exec.LookPath("aplay"); err != nil {
		return modem.AudioCapabilities{}, fmt.Errorf("aplay is unavailable: %w", err)
	}
	return modem.AudioCapabilities{Backend: "alsa-uac", SampleRate: SampleRate, Channels: Channels}, nil
}

// PrepareRoute refreshes the QDC507's modem-side VoLTE/UAC route before a
// call. This is optional for ordinary ALSA devices and enabled only when the
// deployment supplies CELLBRIDGE_ROUTE_HELPER.
func (a *ALSAAudio) PrepareRoute(ctx context.Context) error {
	return prepareExternalRoute(ctx)
}

// snapshot returns the current pair for IO.
func (a *ALSAAudio) snapshot() (rx io.ReadCloser, tx io.WriteCloser, callID modem.CallID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rx, a.tx, a.call
}

// reap kills and waits for a child OUTSIDE a.mu. Wait can block if the
// child ignores signals or its pipe is wedged; it must never hold the
// lock.
func reap(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-waitTimeout():
	}
}

// waitDeadline is a package-level var so tests can shorten it.
var waitTimeout = func() <-chan time.Time {
	return time.After(3 * time.Second)
}

func (a *ALSAAudio) Start(ctx context.Context, callID modem.CallID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.call != "" && a.call != callID {
		current := a.call
		a.mu.Unlock()
		return fmt.Errorf("ALSA audio is already active for call %s", current)
	}
	if a.call == callID {
		a.mu.Unlock()
		return nil
	}
	// Take ownership, then release the lock before touching children.
	a.call = callID
	capture, playback, rx, tx := a.capture, a.playback, a.rx, a.tx
	a.capture, a.playback, a.rx, a.tx = nil, nil, nil, nil
	a.mu.Unlock()

	// Kill any stale pair outside the lock.
	reap(capture)
	reap(playback)
	if rx != nil {
		_ = rx.Close()
	}
	if tx != nil {
		_ = tx.Close()
	}

	newCapture := exec.Command("arecord", "-q", "-D", a.captureDevice, "-t", "raw", "-f", "S16_LE", "-r", fmt.Sprint(SampleRate), "-c", fmt.Sprint(Channels), "--buffer-size=8192", "--period-size=1024")
	newPlayback := exec.Command("aplay", "-q", "-D", a.playbackDevice, "-t", "raw", "-f", "S16_LE", "-r", fmt.Sprint(SampleRate), "-c", fmt.Sprint(Channels), "--buffer-size=640", "--period-size=160")
	newCapture.Stderr = io.Discard
	newPlayback.Stderr = io.Discard
	newRx, err := newCapture.StdoutPipe()
	if err != nil {
		a.releaseCall(callID)
		return fmt.Errorf("create ALSA capture pipe: %w", err)
	}
	newTx, err := newPlayback.StdinPipe()
	if err != nil {
		_ = newRx.Close()
		a.releaseCall(callID)
		return fmt.Errorf("create ALSA playback pipe: %w", err)
	}
	// Critical ordering on the QDC507 UAC gadget: the capture stream must
	// start FIRST. If aplay starts first, the full-duplex ASYNC clock
	// locks onto the playback side and the capture reads silence forever
	// (verified 2026-09-06: manual arecord-only = full-scale VoLTE voice;
	// aplay-first pair = zeros).
	if err := newCapture.Start(); err != nil {
		_ = newTx.Close()
		_ = newRx.Close()
		reap(newPlayback)
		a.releaseCall(callID)
		return fmt.Errorf("start ALSA capture: %w", err)
	}
	if err := newPlayback.Start(); err != nil {
		reap(newCapture) // Do not leave arecord holding the UAC device on failure.
		_ = newRx.Close()
		_ = newTx.Close()
		a.releaseCall(callID)
		return fmt.Errorf("start ALSA playback: %w", err)
	}
	a.mu.Lock()
	a.capture, a.playback, a.rx, a.tx = newCapture, newPlayback, newRx, newTx
	a.mu.Unlock()
	// Bridge.clientPlaybackLoop is the single 20ms playback clock. It writes
	// silence whenever RTP is absent, keeping UAC full-duplex RUNNING without
	// a second writer that can overfill aplay's pipe and add seconds of delay.
	slog.Info("alsa pair started", "call_id", callID, "capture", a.captureDevice, "playback", a.playbackDevice)
	return nil
}

func (a *ALSAAudio) releaseCall(callID modem.CallID) {
	a.mu.Lock()
	if a.call == callID {
		a.call = ""
	}
	a.mu.Unlock()
}

func (a *ALSAAudio) ReadPCM(samples []int16) (int, error) {
	rx, _, callID := a.snapshot()
	if callID == "" || rx == nil {
		return 0, fmt.Errorf("ALSA audio is not active")
	}
	buffer := make([]byte, len(samples)*2)
	count, err := io.ReadFull(rx, buffer)
	for index := 0; index+1 < count; index += 2 {
		samples[index/2] = int16(binary.LittleEndian.Uint16(buffer[index : index+2]))
	}
	return count / 2, err
}

func (a *ALSAAudio) WritePCM(samples []int16) (int, error) {
	_, tx, callID := a.snapshot()
	if callID == "" || tx == nil {
		return 0, fmt.Errorf("ALSA audio is not active")
	}
	buffer := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(buffer[index*2:index*2+2], uint16(sample))
	}
	a.txMu.Lock()
	count, err := tx.Write(buffer)
	a.txMu.Unlock()
	return count / 2, err
}

func (a *ALSAAudio) Stop(ctx context.Context) error {
	a.mu.Lock()
	capture, playback, rx, tx := a.capture, a.playback, a.rx, a.tx
	a.capture, a.playback, a.rx, a.tx = nil, nil, nil, nil
	a.call = ""
	a.mu.Unlock()
	reap(capture)
	reap(playback)
	if rx != nil {
		_ = rx.Close()
	}
	if tx != nil {
		_ = tx.Close()
	}
	return nil
}

func (a *ALSAAudio) Close() error {
	var err error
	a.once.Do(func() {
		err = a.Stop(context.Background())
	})
	return err
}
