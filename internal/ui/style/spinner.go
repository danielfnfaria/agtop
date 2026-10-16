package style

// SpinnerFrameMS is the duration of one spinner frame. The frame comes from the time, not from
// a tick counter.
const SpinnerFrameMS = 160

// SpinnerFrame returns the frame index at nowMS, for a spinner of the given number of frames.
// Negative times still give a valid frame.
func SpinnerFrame(nowMS int64, frames int) int {
	q := nowMS / SpinnerFrameMS
	if nowMS%SpinnerFrameMS < 0 {
		q--
	}
	n := int(q % int64(frames))
	if n < 0 {
		n += frames
	}
	return n
}
