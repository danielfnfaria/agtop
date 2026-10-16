//go:build !darwin

package watcher

import "errors"

// inotify watches a directory with a single descriptor: fsnotify is enough.
const nativeDirWatch = false

func newDirWatch(string, func(string) bool) (dirWatch, error) {
	return nil, errors.New("watcher: no native directory watch on this platform")
}
