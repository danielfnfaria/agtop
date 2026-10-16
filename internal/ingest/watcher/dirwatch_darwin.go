//go:build darwin

package watcher

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"
)

// fsnotify's kqueue backend opens a descriptor per entry of a watched directory. This one opens
// the directory alone (plus the entries watchFile accepts) and re-lists it on each change.
const nativeDirWatch = true

const (
	dirNotes  = unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_EXTEND | unix.NOTE_ATTRIB
	goneNotes = unix.NOTE_DELETE | unix.NOTE_RENAME
	wakeIdent = 0
)

type entryStat struct {
	size  int64
	mtime int64
}

type kqDirWatch struct {
	dir       string
	watchFile func(name string) bool
	kq        int
	events    chan fsnotify.Event
	errs      chan error
	done      chan struct{}
	exited    chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	closed  bool
	dirfd   int
	files   map[int]string
	fileFds map[string]int
	entries map[string]entryStat
}

func newDirWatch(dir string, watchFile func(string) bool) (dirWatch, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(kq)
	w := &kqDirWatch{
		dir:       dir,
		watchFile: watchFile,
		kq:        kq,
		events:    make(chan fsnotify.Event),
		errs:      make(chan error),
		done:      make(chan struct{}),
		exited:    make(chan struct{}),
		dirfd:     -1,
		files:     map[int]string{},
		fileFds:   map[string]int{},
		entries:   map[string]entryStat{},
	}
	var wake unix.Kevent_t
	unix.SetKevent(&wake, wakeIdent, unix.EVFILT_USER, unix.EV_ADD|unix.EV_CLEAR)
	if err := w.register(wake); err != nil {
		_ = unix.Close(kq)
		return nil, err
	}
	w.mu.Lock()
	err = w.openDirLocked()
	if err == nil {
		_, err = w.relistLocked()
	}
	w.mu.Unlock()
	if err != nil {
		w.release()
		return nil, err
	}
	go w.run()
	return w, nil
}

func (w *kqDirWatch) register(ev unix.Kevent_t) error {
	for {
		_, err := unix.Kevent(w.kq, []unix.Kevent_t{ev}, nil, nil)
		if err != unix.EINTR {
			return err
		}
	}
}

func (w *kqDirWatch) wake() {
	var ev unix.Kevent_t
	unix.SetKevent(&ev, wakeIdent, unix.EVFILT_USER, 0)
	ev.Fflags = unix.NOTE_TRIGGER
	_ = w.register(ev)
}

func openEvtOnly(p string, extra int) (int, error) {
	for {
		fd, err := unix.Open(p, unix.O_EVTONLY|unix.O_CLOEXEC|unix.O_NONBLOCK|extra, 0)
		if err != unix.EINTR {
			return fd, err
		}
	}
}

func (w *kqDirWatch) watchVnode(fd int) error {
	var ev unix.Kevent_t
	unix.SetKevent(&ev, fd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_CLEAR)
	ev.Fflags = dirNotes
	return w.register(ev)
}

func (w *kqDirWatch) openDirLocked() error {
	fd, err := openEvtOnly(w.dir, unix.O_DIRECTORY)
	if err != nil {
		return &os.PathError{Op: "open", Path: w.dir, Err: err}
	}
	if err := w.watchVnode(fd); err != nil {
		_ = unix.Close(fd)
		return err
	}
	w.dirfd = fd
	return nil
}

func (w *kqDirWatch) closeDirLocked() {
	if w.dirfd >= 0 {
		_ = unix.Close(w.dirfd)
		w.dirfd = -1
	}
}

func (w *kqDirWatch) watchFileLocked(name string) {
	if _, ok := w.fileFds[name]; ok {
		return
	}
	fd, err := openEvtOnly(filepath.Join(w.dir, name), 0)
	if err != nil {
		return // gone already: the next listing notices
	}
	if err := w.watchVnode(fd); err != nil {
		_ = unix.Close(fd)
		return
	}
	w.files[fd] = name
	w.fileFds[name] = fd
}

func (w *kqDirWatch) unwatchFileLocked(name string) {
	if fd, ok := w.fileFds[name]; ok {
		_ = unix.Close(fd)
		delete(w.files, fd)
		delete(w.fileFds, name)
	}
}

// relistLocked lists the directory again and turns the differences into events.
func (w *kqDirWatch) relistLocked() ([]fsnotify.Event, error) {
	ents, err := os.ReadDir(w.dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var out []fsnotify.Event
	next := make(map[string]entryStat, len(ents))
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		name := ent.Name()
		st := entryStat{size: info.Size(), mtime: info.ModTime().UnixNano()}
		next[name] = st
		p := filepath.Join(w.dir, name)
		if prev, ok := w.entries[name]; !ok {
			out = append(out, fsnotify.Event{Name: p, Op: fsnotify.Create})
		} else if prev != st {
			out = append(out, fsnotify.Event{Name: p, Op: fsnotify.Write})
		}
		if w.watchFile != nil && ent.Type().IsRegular() && w.watchFile(name) {
			w.watchFileLocked(name)
		}
	}
	for name := range w.entries {
		if _, ok := next[name]; !ok {
			w.unwatchFileLocked(name)
			out = append(out, fsnotify.Event{Name: filepath.Join(w.dir, name), Op: fsnotify.Remove})
		}
	}
	w.entries = next
	return out, nil
}

func (w *kqDirWatch) run() {
	defer close(w.exited)
	buf := make([]unix.Kevent_t, 64)
	for {
		n, err := unix.Kevent(w.kq, nil, buf, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			w.sendErr(err)
			return
		}
		var out []fsnotify.Event
		relist := false
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return
		}
		for _, ev := range buf[:n] {
			fd := int(ev.Ident)
			switch {
			case ev.Filter == unix.EVFILT_USER:
				relist = true
			case fd == w.dirfd:
				if ev.Fflags&goneNotes != 0 {
					w.closeDirLocked()
					out = append(out, fsnotify.Event{Name: w.dir, Op: fsnotify.Remove})
				}
				relist = true
			default:
				name, ok := w.files[fd]
				if !ok {
					continue
				}
				if ev.Fflags&goneNotes != 0 {
					w.unwatchFileLocked(name)
					relist = true
					continue
				}
				var st unix.Stat_t
				if unix.Fstat(fd, &st) == nil {
					if _, known := w.entries[name]; known {
						w.entries[name] = entryStat{size: st.Size, mtime: unix.TimespecToNsec(st.Mtim)}
					}
				}
				out = append(out, fsnotify.Event{Name: filepath.Join(w.dir, name), Op: fsnotify.Write})
			}
		}
		var relistErr error
		if relist {
			var evs []fsnotify.Event
			evs, relistErr = w.relistLocked()
			out = append(out, evs...)
		}
		w.mu.Unlock()
		for _, ev := range out {
			select {
			case w.events <- ev:
			case <-w.done:
				return
			}
		}
		if relistErr != nil && !w.sendErr(relistErr) {
			return
		}
	}
}

func (w *kqDirWatch) sendErr(err error) bool {
	select {
	case w.errs <- err:
		return true
	case <-w.done:
		return false
	}
}

func (w *kqDirWatch) Events() <-chan fsnotify.Event { return w.events }
func (w *kqDirWatch) Errors() <-chan error          { return w.errs }

// Rewatch reopens the directory (it may have been replaced) and lists it again.
func (w *kqDirWatch) Rewatch() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closeDirLocked()
	err := w.openDirLocked()
	w.mu.Unlock()
	w.wake()
	return err
}

func (w *kqDirWatch) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.dirfd < 0 {
		return 0
	}
	return 1
}

func (w *kqDirWatch) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		w.mu.Unlock()
		close(w.done)
		w.wake()
		<-w.exited
		w.release()
	})
	return nil
}

// release closes every descriptor; the run goroutine is gone (or never started).
func (w *kqDirWatch) release() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeDirLocked()
	for name := range w.fileFds {
		w.unwatchFileLocked(name)
	}
	_ = unix.Close(w.kq)
}
