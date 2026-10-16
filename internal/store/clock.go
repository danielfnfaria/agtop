package store

import "time"

type realClock struct{}

func (realClock) Now() int64 { return time.Now().UnixMilli() }

func (realClock) AfterFunc(ms int64, f func()) func() {
	t := time.AfterFunc(time.Duration(ms)*time.Millisecond, f)
	return func() { t.Stop() }
}
