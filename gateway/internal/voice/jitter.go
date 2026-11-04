package voice

// jitterBuffer orders the phone's uplink RTP frames by sequence number and
// hands exactly one frame to the 20 ms modem playback clock per tick.
//
// The previous FIFO played frames in arrival order, so a reordered packet was
// played late (audible as a stutter), a duplicate was played twice, and every
// late arrival became a hard 20 ms silence gap because nothing was buffered
// ahead of the playback clock. This buffer:
//
//   - holds jitterTarget frames (40 ms) before playing, and again after an
//     underrun, so ordinary Wi-Fi/LTE arrival jitter does not starve playback;
//   - drops duplicates and frames that arrive after their slot was played;
//   - conceals a missing frame by replaying the last good one at falling gain
//     (two frames at most, then silence) instead of inserting a click;
//   - caps queued audio at jitterMax frames (120 ms) so clock drift or a burst
//     after a stall cannot grow the mouth-to-ear delay without bound.
//
// It is not safe for concurrent use; Bridge guards it with its mutex.
type jitterBuffer struct {
	frames    map[uint16][]int16
	next      uint16
	started   bool
	buffering bool
	last      []int16
	concealed int
	stats     jitterStats
}

type jitterStats struct {
	Received   uint64
	Late       uint64
	Duplicate  uint64
	Lost       uint64
	Concealed  uint64
	Underruns  uint64
	Overflowed uint64
	Resets     uint64
}

const (
	jitterTarget = 2  // 40 ms of prefill
	jitterMax    = 6  // 120 ms latency ceiling
	jitterReset  = 50 // a sequence jump of 1 s or more means a new stream
	jitterFade   = 2  // concealment frames before falling silent
)

func newJitterBuffer() *jitterBuffer {
	return &jitterBuffer{frames: make(map[uint16][]int16)}
}

func (j *jitterBuffer) reset(seq uint16) {
	clear(j.frames)
	j.next = seq
	j.started = true
	j.buffering = true
}

// push stores one decoded frame. The slice is copied.
func (j *jitterBuffer) push(seq uint16, pcm []int16) {
	if !j.started {
		j.reset(seq)
	}
	delta := int16(seq - j.next)
	if delta <= -jitterReset || delta >= jitterReset {
		// SSRC change, sender restart or a very long outage.
		j.stats.Resets++
		j.reset(seq)
		delta = 0
	}
	if delta < 0 {
		j.stats.Late++
		return
	}
	if _, ok := j.frames[seq]; ok {
		j.stats.Duplicate++
		return
	}
	frame := make([]int16, len(pcm))
	copy(frame, pcm)
	j.frames[seq] = frame
	j.stats.Received++
	// Bound latency: skip the oldest slots until the newest frame is at most
	// jitterMax ahead of the playback position.
	for int16(seq-j.next) >= jitterMax {
		if _, ok := j.frames[j.next]; ok {
			delete(j.frames, j.next)
			j.stats.Overflowed++
		}
		j.next++
	}
}

// pop returns the frame for the current 20 ms tick. It never returns nil when
// silence is non-nil: missing audio is concealed or replaced by silence.
func (j *jitterBuffer) pop(silence []int16) []int16 {
	if !j.started {
		return silence
	}
	if j.buffering {
		if len(j.frames) < jitterTarget {
			return j.conceal(silence)
		}
		j.buffering = false
	}
	if frame, ok := j.frames[j.next]; ok {
		delete(j.frames, j.next)
		j.next++
		j.last = frame
		j.concealed = 0
		return frame
	}
	if len(j.frames) == 0 {
		// Nothing queued at all: the sender is behind. Refill before resuming.
		j.stats.Underruns++
		j.buffering = true
		return j.conceal(silence)
	}
	if len(j.frames) < jitterTarget {
		// A later frame is here but the expected one may only be reordered;
		// give it one tick before declaring it lost.
		return j.conceal(silence)
	}
	j.stats.Lost++
	j.next++
	return j.conceal(silence)
}

func (j *jitterBuffer) conceal(silence []int16) []int16 {
	if j.last == nil || j.concealed >= jitterFade {
		return silence
	}
	j.concealed++
	j.stats.Concealed++
	shift := uint(j.concealed) // 1/2 gain, then 1/4
	out := make([]int16, len(j.last))
	for i, sample := range j.last {
		out[i] = sample >> shift
	}
	return out
}
