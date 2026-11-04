package voice

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/cellbridge/cellbridge/gateway/internal/modem"
)

type PCMUTransport interface {
	WritePCMU([]byte) error
	OnPCMUFrame(func([]byte))
}

// SequencedPCMUTransport is implemented by RTP media that can report each
// frame's RTP sequence number, letting the jitter buffer reorder and drop
// duplicates. Transports without it get arrival-order sequence numbers.
type SequencedPCMUTransport interface {
	OnSequencedPCMUFrame(func(seq uint16, frame []byte))
}

// Bridge is the only place where the modem's signed PCM representation is
// converted to the V1 WebRTC PCMU wire format. It enforces 20 ms / 160-sample
// frames in both directions and keeps media work off HTTP handlers.
type Bridge struct {
	audio             modem.VoiceAudio
	media             PCMUTransport
	cellularFrame     func([]int16)
	clientFrame       func([]int16)
	stop              sync.Once
	started           bool
	callID            modem.CallID
	clientFrameSeen   bool
	cellularFrameSeen bool
	cellularFrames    uint64
	clientFrames      uint64
	cellularPeakMax   int
	clientPeakMax     int
	cellularNonZero   uint64
	clientNonZero     uint64
	clientJitter      *jitterBuffer
	cellularSum       uint64
	clientSum         uint64
	cellularWindowAt  time.Time
	clientWindowAt    time.Time
	mu                sync.Mutex
	// writeMu serialises modem playback writes with Stop. It is separate from
	// mu so a write blocked on aplay never stalls the RTP receive callback,
	// which used to hold back packets and release them as artificial bursts.
	writeMu sync.Mutex
}

func NewBridge(audio modem.VoiceAudio, media PCMUTransport) *Bridge {
	return &Bridge{audio: audio, media: media}
}

func (b *Bridge) SetFrameHooks(cellularFrame, clientFrame func([]int16)) {
	b.mu.Lock()
	b.cellularFrame, b.clientFrame = cellularFrame, clientFrame
	b.mu.Unlock()
}

func (b *Bridge) Start(ctx context.Context, callID modem.CallID) error {
	if b.audio == nil || b.media == nil {
		return fmt.Errorf("voice audio and WebRTC media are required")
	}
	if err := b.audio.Start(ctx, callID); err != nil {
		return err
	}
	b.mu.Lock()
	b.started = true
	b.callID = callID
	b.clientFrameSeen = false
	b.cellularFrameSeen = false
	b.clientJitter = newJitterBuffer()
	b.clientFrames, b.cellularFrames = 0, 0
	b.clientPeakMax, b.cellularPeakMax = 0, 0
	b.clientNonZero, b.cellularNonZero = 0, 0
	b.clientSum, b.cellularSum = 0, 0
	b.clientWindowAt, b.cellularWindowAt = time.Now(), time.Now()
	b.mu.Unlock()
	onFrame := func(seq uint16, frame []byte) {
		if len(frame) != FrameSamples {
			slog.Warn("voice client frame dropped", "call_id", callID, "samples", len(frame))
			return
		}
		pcm, err := DecodePCMU(frame)
		if err != nil {
			slog.Warn("voice client frame decode failed", "call_id", callID, "error", err)
			return
		}
		b.mu.Lock()
		if !b.started {
			b.mu.Unlock()
			return
		}
		if !b.clientFrameSeen {
			b.clientFrameSeen = true
			slog.Info("voice client audio frame received", "call_id", callID, "pcm_peak", pcmPeak(pcm))
		}
		cpeak := pcmPeak(pcm)
		b.clientFrames++
		if cpeak > b.clientPeakMax {
			b.clientPeakMax = cpeak
		}
		if cpeak > 0 {
			b.clientNonZero++
		}
		b.clientSum += uint64(cpeak)
		if b.clientFrames%voiceStatsFrames == 0 {
			now := time.Now()
			elapsed := now.Sub(b.clientWindowAt)
			b.clientWindowAt = now
			slog.Info("voice client stats", "call_id", callID, "frames", b.clientFrames, "window_frames", voiceStatsFrames, "peak_max", b.clientPeakMax, "nonzero", b.clientNonZero, "mean", b.clientSum/voiceStatsFrames, "win_ms", elapsed.Milliseconds())
			b.clientPeakMax = 0
			b.clientNonZero, b.clientSum = 0, 0
		}
		clientHook := b.clientFrame
		b.mu.Unlock()
		if clientHook != nil {
			clientHook(pcm)
		}
		// Jitter buffer (2026-09-06): enqueue the decoded frame; a fixed
		// 20ms ticker drains it into aplay so playback never stalls.
		// Without this, iPhone RTP bursts/stalls propagate through the
		// UAC ADAPTIVE playback clock and choke the ASYNC capture side
		// (observed: 50-frame windows taking 3-7s, then burst refills).
		b.enqueueClient(seq, pcm)
	}
	if sequenced, ok := b.media.(SequencedPCMUTransport); ok {
		sequenced.OnSequencedPCMUFrame(onFrame)
	} else {
		var arrival uint16
		b.media.OnPCMUFrame(func(frame []byte) {
			onFrame(arrival, frame)
			arrival++
		})
	}
	go b.readLoop(ctx, callID)
	go b.clientPlaybackLoop(ctx, callID)
	return nil
}

const voiceStatsFrames = 250 // Five-second diagnostic windows, not per-second log traffic.

// enqueueClient hands a decoded upstream frame to the sequence-ordered
// jitter buffer between the RTP callback and the fixed-rate playback ticker
// (see jitter.go for prefill, concealment and the latency ceiling).
func (b *Bridge) enqueueClient(seq uint16, pcm []int16) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.clientJitter != nil {
		b.clientJitter.push(seq, pcm)
	}
}

// clientPlaybackLoop drains the jitter buffer at a fixed 20ms cadence,
// writing concealment or silence when no frame is due (keeps UAC playback
// RUNNING and the full-duplex clock moving — no XRUN, no capture stall).
//
// Frames are paced against an absolute schedule (start + n·20 ms), not a
// time.Ticker. A ticker silently drops ticks whenever a write to aplay blocks
// past the next one, so playback ran ~3-4 % slower than the phone's 50 fps
// stream and the jitter buffer overflowed ~100 frames per call (2026-11-04).
// Missed slots are now caught up; after a long stall the schedule is reset
// instead of bursting audio into the modem.
func (b *Bridge) clientPlaybackLoop(ctx context.Context, callID modem.CallID) {
	silence := make([]int16, FrameSamples)
	start := time.Now()
	var played int64
	timer := time.NewTimer(0)
	defer timer.Stop()
	<-timer.C
	for {
		due := start.Add(time.Duration(played+1) * frameInterval)
		if wait := time.Until(due); wait > 0 {
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		} else if -wait > playbackResync {
			start, played = time.Now(), 0
		}
		if ctx.Err() != nil {
			return
		}
		b.mu.Lock()
		if !b.started {
			b.mu.Unlock()
			return
		}
		frame := b.clientJitter.pop(silence)
		b.mu.Unlock()
		if err := b.writePCM(frame); err != nil {
			slog.Warn("voice modem playback failed", "call_id", callID, "error", err)
		}
		played++
	}
}

const (
	frameInterval  = time.Duration(FrameMillis) * time.Millisecond
	playbackResync = 200 * time.Millisecond
)

func (b *Bridge) readLoop(ctx context.Context, callID modem.CallID) {
	pcm := make([]int16, FrameSamples)
	consecutivePlaybackErrors := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		count, err := b.audio.ReadPCM(pcm)
		if err != nil {
			if b.isStarted() {
				slog.Warn("voice modem capture failed", "call_id", callID, "error", err)
			}
			return
		}
		if !b.isStarted() {
			return
		}
		if count != FrameSamples {
			continue
		}
		b.mu.Lock()
		cellularHook := b.cellularFrame
		b.mu.Unlock()
		if cellularHook != nil {
			cellularHook(pcm)
		}
		b.mu.Lock()
		if !b.cellularFrameSeen {
			b.cellularFrameSeen = true
			slog.Info("voice cellular audio frame received", "call_id", callID, "pcm_peak", pcmPeak(pcm))
		}
		peak := pcmPeak(pcm)
		b.cellularFrames++
		if peak > b.cellularPeakMax {
			b.cellularPeakMax = peak
		}
		if peak > 0 {
			b.cellularNonZero++
		}
		b.cellularSum += uint64(peak)
		if b.cellularFrames%voiceStatsFrames == 0 {
			now := time.Now()
			elapsed := now.Sub(b.cellularWindowAt)
			b.cellularWindowAt = now
			slog.Info("voice cellular stats", "call_id", callID, "frames", b.cellularFrames, "window_frames", voiceStatsFrames, "peak_max", b.cellularPeakMax, "nonzero", b.cellularNonZero, "mean", b.cellularSum/voiceStatsFrames, "win_ms", elapsed.Milliseconds())
			b.cellularPeakMax = 0
			b.cellularNonZero, b.cellularSum = 0, 0
		}
		b.mu.Unlock()
		frame, err := EncodePCMU(pcm)
		if err != nil {
			slog.Warn("voice cellular frame encode failed", "call_id", callID, "error", err)
			return
		}
		if err := b.media.WritePCMU(frame); err != nil {
			consecutivePlaybackErrors++
			// A SIP client may advertise its RTP port before it has processed our
			// final 200 OK. On macOS that short window can return EHOSTUNREACH.
			// Keep the capture loop alive: later 20 ms frames will succeed after
			// the client sends ACK. Rate-limit warnings if the failure persists.
			if b.isStarted() && (consecutivePlaybackErrors == 1 || consecutivePlaybackErrors%50 == 0) {
				slog.Warn("voice client playback failed", "call_id", callID, "error", err)
			}
			continue
		}
		if consecutivePlaybackErrors > 0 {
			slog.Info("voice client playback recovered", "call_id", callID, "failed_frames", consecutivePlaybackErrors)
			consecutivePlaybackErrors = 0
		}
	}
}

func (b *Bridge) isStarted() bool {
	b.mu.Lock()
	started := b.started
	b.mu.Unlock()
	return started
}

func (b *Bridge) writePCM(pcm []int16) error {
	// Keep the started check and the write serialized with Stop. Otherwise the
	// media callback can enter ALSA after Stop has closed the device and emit a
	// misleading "audio is not active" error during normal call teardown.
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	if !b.isStarted() {
		return nil
	}
	_, err := b.audio.WritePCM(pcm)
	return err
}

func pcmPeak(samples []int16) int {
	peak := 0
	for _, sample := range samples {
		value := int(sample)
		if value < 0 {
			value = -value
		}
		if value > peak {
			peak = value
		}
	}
	return peak
}

func (b *Bridge) Stop(ctx context.Context) error {
	b.stop.Do(func() {
		b.mu.Lock()
		started := b.started
		b.started = false
		callID := b.callID
		b.callID = ""
		var uplink jitterStats
		if b.clientJitter != nil {
			uplink = b.clientJitter.stats
		}
		b.mu.Unlock()
		if started {
			// Uplink (phone → cellular) summary for "the other side hears me
			// choppy": lost = RTP gaps, late/duplicate = reordering on the
			// path, underruns = the phone's stream stalled.
			slog.Info("voice bridge stopping", "call_id", callID,
				"uplink_received", uplink.Received, "uplink_lost", uplink.Lost,
				"uplink_late", uplink.Late, "uplink_duplicate", uplink.Duplicate,
				"uplink_concealed", uplink.Concealed, "uplink_underruns", uplink.Underruns,
				"uplink_overflowed", uplink.Overflowed, "uplink_drained", uplink.Drained,
				"uplink_resets", uplink.Resets)
		}
		if started {
			// Wait for an in-flight playback write before stopping the device.
			b.writeMu.Lock()
			_ = b.audio.Stop(ctx)
			b.writeMu.Unlock()
		}
	})
	return nil
}
