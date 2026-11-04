package voice

import "testing"

func jitterFrame(value int16) []int16 {
	frame := make([]int16, FrameSamples)
	for i := range frame {
		frame[i] = value
	}
	return frame
}

func popValues(j *jitterBuffer, ticks int) []int16 {
	silence := make([]int16, FrameSamples)
	values := make([]int16, 0, ticks)
	for i := 0; i < ticks; i++ {
		values = append(values, j.pop(silence)[0])
	}
	return values
}

func equalValues(got, want []int16) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestJitterBufferPrefillsAndPlaysInOrder(t *testing.T) {
	j := newJitterBuffer()
	j.push(10, jitterFrame(100))
	if got := popValues(j, 1); got[0] != 0 {
		t.Fatalf("played %d before prefill; want silence", got[0])
	}
	// Reordered arrival: 12 before 11.
	j.push(12, jitterFrame(300))
	j.push(11, jitterFrame(200))
	if got, want := popValues(j, 3), []int16{100, 200, 300}; !equalValues(got, want) {
		t.Fatalf("played %v, want %v", got, want)
	}
}

func TestJitterBufferDropsDuplicateAndLateFrames(t *testing.T) {
	j := newJitterBuffer()
	j.push(1, jitterFrame(100))
	j.push(2, jitterFrame(200))
	j.push(2, jitterFrame(999))
	popValues(j, 2)
	j.push(1, jitterFrame(999)) // already played
	if j.stats.Duplicate != 1 || j.stats.Late != 1 {
		t.Fatalf("stats = %+v, want 1 duplicate and 1 late", j.stats)
	}
}

func TestJitterBufferConcealsLostFrame(t *testing.T) {
	j := newJitterBuffer()
	j.push(1, jitterFrame(400))
	j.push(2, jitterFrame(800))
	popValues(j, 2)
	// 3 is lost; 4 and 5 arrive.
	j.push(4, jitterFrame(1600))
	j.push(5, jitterFrame(1600))
	got := popValues(j, 3)
	if want := []int16{400, 1600, 1600}; !equalValues(got, want) {
		t.Fatalf("played %v, want %v (half-gain concealment of 800, then 4, 5)", got, want)
	}
	if j.stats.Lost != 1 || j.stats.Concealed != 1 {
		t.Fatalf("stats = %+v, want 1 lost and 1 concealed", j.stats)
	}
}

func TestJitterBufferFadesToSilenceOnStall(t *testing.T) {
	j := newJitterBuffer()
	j.push(1, jitterFrame(800))
	j.push(2, jitterFrame(800))
	popValues(j, 2)
	if got, want := popValues(j, 4), []int16{400, 200, 0, 0}; !equalValues(got, want) {
		t.Fatalf("stall played %v, want %v", got, want)
	}
	if j.stats.Underruns != 1 {
		t.Fatalf("underruns = %d, want 1", j.stats.Underruns)
	}
	// Resumes only after prefilling again.
	j.push(3, jitterFrame(50))
	if got := popValues(j, 1); got[0] != 0 {
		t.Fatalf("resumed before refill: %d", got[0])
	}
	j.push(4, jitterFrame(60))
	if got, want := popValues(j, 2), []int16{50, 60}; !equalValues(got, want) {
		t.Fatalf("after refill played %v, want %v", got, want)
	}
}

func TestJitterBufferBoundsLatency(t *testing.T) {
	j := newJitterBuffer()
	for seq := uint16(0); seq < 20; seq++ {
		j.push(seq, jitterFrame(int16(seq)))
	}
	if len(j.frames) > jitterMax {
		t.Fatalf("buffered %d frames, ceiling %d", len(j.frames), jitterMax)
	}
	if got := popValues(j, 1); got[0] != 20-jitterMax {
		t.Fatalf("first played frame %d, want %d", got[0], 20-jitterMax)
	}
}

func TestJitterBufferHandlesWrapAndStreamReset(t *testing.T) {
	j := newJitterBuffer()
	j.push(65535, jitterFrame(1))
	j.push(0, jitterFrame(2))
	if got, want := popValues(j, 2), []int16{1, 2}; !equalValues(got, want) {
		t.Fatalf("wrap played %v, want %v", got, want)
	}
	j.push(30000, jitterFrame(7))
	j.push(30001, jitterFrame(8))
	if got, want := popValues(j, 2), []int16{7, 8}; !equalValues(got, want) {
		t.Fatalf("after reset played %v, want %v", got, want)
	}
	if j.stats.Resets != 1 {
		t.Fatalf("resets = %d, want 1", j.stats.Resets)
	}
}

func TestJitterBufferAbsorbsWiFiBurst(t *testing.T) {
	j := newJitterBuffer()
	j.push(0, jitterFrame(1000))
	j.push(1, jitterFrame(1000))
	popValues(j, 2)
	// A 200 ms stall, then the ten held-back frames arrive at once.
	popValues(j, 10)
	for seq := uint16(2); seq < 12; seq++ {
		j.push(seq, jitterFrame(1000))
	}
	if j.stats.Overflowed != 0 {
		t.Fatalf("burst of 10 frames overflowed %d; voice was dropped", j.stats.Overflowed)
	}
}

func TestJitterBufferDrainsDelayInPauses(t *testing.T) {
	j := newJitterBuffer()
	seq := uint16(0)
	// Ten frames queued, then steady 1-in-1-out: the excess must shrink, and
	// only during the quiet (pause) frames.
	for ; seq < 10; seq++ {
		j.push(seq, jitterFrame(0))
	}
	silence := make([]int16, FrameSamples)
	for i := 0; i < 200; i++ {
		j.push(seq, jitterFrame(0))
		seq++
		j.pop(silence)
	}
	if depth := len(j.frames); depth >= jitterDeep {
		t.Fatalf("queue still %d frames deep after 4 s of pauses", depth)
	}
	if j.stats.Drained == 0 || j.stats.Overflowed != 0 {
		t.Fatalf("stats = %+v, want drained frames and no overflow", j.stats)
	}
}

func TestJitterBufferKeepsSpeechWhileDraining(t *testing.T) {
	j := newJitterBuffer()
	seq := uint16(0)
	for ; seq < 8; seq++ {
		j.push(seq, jitterFrame(8000))
	}
	silence := make([]int16, FrameSamples)
	for i := 0; i < drainForce-1; i++ {
		j.push(seq, jitterFrame(8000))
		seq++
		j.pop(silence)
	}
	if j.stats.Drained != 0 {
		t.Fatalf("skipped %d speech frames before the force threshold", j.stats.Drained)
	}
}
