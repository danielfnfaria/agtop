package domain

import "math/bits"

const (
	// ActivityBucketMs is the width of a bucket, in ms.
	ActivityBucketMs = 30_000
	// ActivityWindowBuckets is how many buckets the window keeps (120 x 30 s = 1 h).
	ActivityWindowBuckets = 120
	// SyntheticModel is the model Claude Code uses in synthetic messages (no API call).
	SyntheticModel = "<synthetic>"
	// ActivityMaxLevel is the top of ActivityLevel (8 block steps: 0 = empty, 7 = full).
	ActivityMaxLevel = 7
	// MaxSeenToolUses is how many recent tool_use.id each state remembers to deduplicate.
	MaxSeenToolUses = 512
)

// ActivityHistogram maps the start of a bucket (epoch ms) to the events in it. The operations
// below never mutate their input: they return a new map, or the same one if nothing changed.
type ActivityHistogram map[int64]int

// ActivityBucket returns the start (epoch ms) of the bucket that contains ts.
func ActivityBucket(ts int64) int64 {
	q := ts / ActivityBucketMs
	if ts%ActivityBucketMs < 0 {
		q--
	}
	return q * ActivityBucketMs
}

// WindowStart returns the first bucket of the window that ends at newestBucket.
func WindowStart(newestBucket int64) int64 {
	return newestBucket - (ActivityWindowBuckets-1)*ActivityBucketMs
}

// ActivityWeight is the weight of an assistant line: one per new tool_use, plus one if the line
// opens a new response (its message.id not seen yet; one API response spans several lines).
// Synthetic lines weigh 0.
func ActivityWeight(event AssistantMessageEvent, newResponse bool, newToolUses int) int {
	if event.Model == SyntheticModel {
		return 0
	}
	if newResponse {
		return newToolUses + 1
	}
	return newToolUses
}

// PruneActivity removes the buckets before minBucket.
func PruneActivity(h ActivityHistogram, minBucket int64) ActivityHistogram {
	var out ActivityHistogram
	for bucket := range h {
		if bucket >= minBucket {
			continue
		}
		if out == nil {
			out = cloneHistogram(h)
		}
		delete(out, bucket)
	}
	if out == nil {
		return h
	}
	return out
}

func cloneHistogram(h ActivityHistogram) ActivityHistogram {
	out := make(ActivityHistogram, len(h)+1)
	for k, v := range h {
		out[k] = v
	}
	return out
}

// AddActivity adds weight to the bucket of ts and prunes what fell out of the window that ends
// at the newest bucket. A weight <= 0 returns h.
func AddActivity(h ActivityHistogram, ts int64, weight int) ActivityHistogram {
	if weight <= 0 {
		return h
	}
	bucket := ActivityBucket(ts)
	out := cloneHistogram(h)
	out[bucket] += weight
	newest := bucket
	for b := range out {
		if b > newest {
			newest = b
		}
	}
	return PruneActivity(out, WindowStart(newest))
}

// MergeActivity sums two histograms bucket by bucket (a new map, unless one is empty).
func MergeActivity(a, b ActivityHistogram) ActivityHistogram {
	if len(b) == 0 {
		return a
	}
	if len(a) == 0 {
		return b
	}
	out := cloneHistogram(a)
	for bucket, n := range b {
		out[bucket] += n
	}
	return out
}

// ActivityLevel is a fixed log2 scale: 0 -> 0, 1 -> 1, 2-3 -> 2, 4-7 -> 3, ..., 64+ -> 7.
func ActivityLevel(n int) int {
	if n < 1 {
		return 0
	}
	return min(ActivityMaxLevel, bits.Len(uint(n)))
}

// ActivityCell is one chart column: the level and the profile with most events in the bucket
// (empty if the bucket is).
type ActivityCell struct {
	Level   int
	Profile string
}

// ProfileHistogram is the histogram of one profile label.
type ProfileHistogram struct {
	Label     string
	Histogram ActivityHistogram
}

// ActivityCells returns width cells, from the oldest to the one of nowBucket. The level comes
// from the sum over the profiles; the profile is the dominant one of the bucket (a tie goes to
// the first in byProfile). Buckets after nowBucket are left out; a nowBucket that is not a
// bucket start is aligned.
func ActivityCells(byProfile []ProfileHistogram, nowBucket int64, width int) []ActivityCell {
	end := ActivityBucket(nowBucket)
	cells := make([]ActivityCell, 0, max(width, 0))
	for i := width - 1; i >= 0; i-- {
		bucket := end - int64(i)*ActivityBucketMs
		total, best := 0, 0
		profile := ""
		for _, p := range byProfile {
			n := p.Histogram[bucket]
			total += n
			if n > best {
				best = n
				profile = p.Label
			}
		}
		if profile == "" {
			cells = append(cells, ActivityCell{})
		} else {
			cells = append(cells, ActivityCell{Level: ActivityLevel(total), Profile: profile})
		}
	}
	return cells
}
