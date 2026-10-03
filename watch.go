package conic

import (
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchDebounce is the quiet period after which a burst of file events is
// turned into a single reload. It is a variable so tests can shorten it.
var watchDebounce = 100 * time.Millisecond

// watcher watches the directory of the config file and reloads on change.
type watcher struct {
	s    *state
	fsw  *fsnotify.Watcher
	file string // absolute, cleaned config file path
	wait time.Duration

	quit     chan struct{}
	finished chan struct{}
	stopOnce sync.Once
}

// WatchConfig starts watching the config file and reloads it automatically
// when it changes. OnConfigLoad hooks run after each successful reload and
// OnConfigError hooks after a failed one (the previous values are kept).
//
// The directory containing the file is watched, so atomic saves (write a
// temporary file, then rename) and symlink swaps such as Kubernetes ConfigMap
// updates are detected. Bursts of events are coalesced. Changes whose content
// equals what was last read or written (e.g. by WriteConfig) are ignored.
//
// Calling WatchConfig while already watching is a no-op that returns nil. All
// Sub views share the watcher. Stop it with StopWatch.
func (c *Conic) WatchConfig() error {
	s := c.s
	if _, _, err := s.prepare(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watcher != nil {
		return nil
	}
	file, _, err := s.prepareLocked()
	if err != nil {
		return err
	}
	file, err = filepath.Abs(file)
	if err != nil {
		return ConfigFileReadError{Path: file, Err: err}
	}
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return ConfigFileReadError{Path: file, Err: err}
	}
	if err := fsw.Add(filepath.Dir(file)); err != nil {
		_ = fsw.Close()
		return ConfigFileReadError{Path: file, Err: err}
	}
	w := &watcher{
		s:        s,
		fsw:      fsw,
		file:     file,
		wait:     watchDebounce,
		quit:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	s.watcher = w
	go w.run()
	return nil
}

// WatchConfig is Conic.WatchConfig on the default instance.
func WatchConfig() error { return std.WatchConfig() }

// StopWatch stops watching. When it returns, the event goroutine has exited and
// no hook will be called any more. It is a no-op if not watching, and must not
// be called from within a hook.
func (c *Conic) StopWatch() {
	s := c.s
	s.mu.Lock()
	w := s.watcher
	s.watcher = nil
	s.mu.Unlock()
	if w != nil {
		w.stop()
	}
}

// StopWatch is Conic.StopWatch on the default instance.
func StopWatch() { std.StopWatch() }

func (w *watcher) stop() {
	w.stopOnce.Do(func() {
		close(w.quit)
		_ = w.fsw.Close()
	})
	<-w.finished
}

func (w *watcher) run() {
	defer close(w.finished)

	realFile, _ := filepath.EvalSymlinks(w.file)

	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	schedule := func() {
		if timer == nil {
			timer = time.NewTimer(w.wait)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.wait)
		}
		timerC = timer.C
	}

	const relevant = fsnotify.Write | fsnotify.Create | fsnotify.Rename | fsnotify.Remove
	for {
		select {
		case <-w.quit:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			cur, _ := filepath.EvalSymlinks(w.file)
			if (filepath.Clean(ev.Name) == w.file && ev.Op&relevant != 0) ||
				(cur != "" && cur != realFile) {
				realFile = cur
				schedule()
			}
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			if w.stopped() {
				return
			}
			w.s.logf("conic: watch error: %v", err)
			w.s.fireError(err)
		case <-timerC:
			timerC = nil
			if w.stopped() {
				return
			}
			w.reload()
		}
	}
}

func (w *watcher) stopped() bool {
	select {
	case <-w.quit:
		return true
	default:
		return false
	}
}

// reload re-reads the file and applies it. It runs on the watcher goroutine,
// without s.mu held.
func (w *watcher) reload() {
	s := w.s
	path, _, err := s.prepare()
	if err != nil {
		s.fireError(err)
		return
	}
	b, err := readFile(path)
	if err != nil {
		var nf ConfigFileNotFoundError
		if errors.As(err, &nf) {
			s.logf("conic: config file %q is missing, waiting for it to reappear", path)
			return
		}
		s.fireError(err)
		return
	}
	if s.sameAsLast(b) {
		return
	}
	if err := s.load(b); err != nil {
		s.logf("conic: reloading config file %q failed: %v", path, err)
		s.fireError(err)
		return
	}
	s.logf("conic: reloaded config file %q", path)
	s.fireLoad()
}
