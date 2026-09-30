package voice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cellbridge/cellbridge/gateway/internal/modem"
)

type fakeAudio struct {
	mu        sync.Mutex
	started   bool
	frames    int
	maxFrames int
}

func (a *fakeAudio) Probe(context.Context) (modem.AudioCapabilities, error) {
	return modem.AudioCapabilities{Backend: "fake", SampleRate: 8000, Channels: 1}, nil
}
func (a *fakeAudio) Start(context.Context, modem.CallID) error {
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	return nil
}
func (a *fakeAudio) ReadPCM(pcm []int16) (int, error) {
	for index := range pcm {
		pcm[index] = int16(index * 10)
	}
	a.mu.Lock()
	a.frames++
	count := a.frames
	maxFrames := a.maxFrames
	a.mu.Unlock()
	if maxFrames == 0 {
		maxFrames = 1
	}
	if count > maxFrames {
		return len(pcm), context.Canceled
	}
	return len(pcm), nil
}
func (a *fakeAudio) WritePCM(pcm []int16) (int, error) { return len(pcm), nil }
func (a *fakeAudio) Stop(context.Context) error        { return nil }
func (a *fakeAudio) Close() error                      { return nil }

type fakeMedia struct {
	mu         sync.Mutex
	writer     func([]byte)
	frames     [][]byte
	writeCalls int
	failWrites int
}

func (m *fakeMedia) WritePCMU(frame []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeCalls++
	if m.failWrites > 0 {
		m.failWrites--
		return errors.New("temporary RTP route failure")
	}
	m.frames = append(m.frames, frame)
	return nil
}

func TestBridgeRetriesAfterTemporaryClientPlaybackFailure(t *testing.T) {
	audio := &fakeAudio{maxFrames: 3}
	media := &fakeMedia{failWrites: 1}
	bridge := NewBridge(audio, media)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bridge.Start(ctx, "call-retry"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		media.mu.Lock()
		calls, successes := media.writeCalls, len(media.frames)
		media.mu.Unlock()
		if calls >= 2 && successes >= 1 {
			_ = bridge.Stop(context.Background())
			return
		}
		time.Sleep(time.Millisecond)
	}
	media.mu.Lock()
	calls, successes := media.writeCalls, len(media.frames)
	media.mu.Unlock()
	t.Fatalf("RTP writes = %d, successes = %d; temporary error stopped the bridge", calls, successes)
}
func (m *fakeMedia) OnPCMUFrame(writer func([]byte)) { m.mu.Lock(); m.writer = writer; m.mu.Unlock() }

func TestBridgeStatsWindowInitializedAndReset(t *testing.T) {
	audio := &fakeAudio{maxFrames: voiceStatsFrames}
	media := &fakeMedia{}
	b := NewBridge(audio, media)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := b.Start(ctx, "stats-test"); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(context.Background())
	media.mu.Lock()
	callback := media.writer
	media.mu.Unlock()
	pcm := make([]int16, FrameSamples)
	for i := range pcm {
		pcm[i] = 1000
	}
	frame, err := EncodePCMU(pcm)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < voiceStatsFrames; i++ {
		callback(frame)
	}
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		ready := b.cellularFrames == voiceStatsFrames
		if ready {
			if b.clientWindowAt.IsZero() || b.cellularWindowAt.IsZero() || time.Since(b.clientWindowAt) > time.Second || b.clientSum != 0 || b.cellularSum != 0 || b.clientNonZero != 0 || b.cellularNonZero != 0 {
				t.Error("stats counters/window did not reset")
			}
		}
		b.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capture test timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBridgeConvertsBothDirections(t *testing.T) {
	audio := &fakeAudio{}
	media := &fakeMedia{}
	bridge := NewBridge(audio, media)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := bridge.Start(ctx, "call-1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	media.mu.Lock()
	writer := media.writer
	frames := len(media.frames)
	media.mu.Unlock()
	if writer == nil || frames == 0 {
		t.Fatalf("media writer=%v frames=%d", writer != nil, frames)
	}
	writer(make([]byte, FrameSamples))
	if err := bridge.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
