package conic

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type watchEnv struct {
	c      *Conic
	path   string
	cfg    server
	loads  chan struct{}
	errs   chan error
	nloads atomic.Int32
}

func shortDebounce(t *testing.T) {
	t.Helper()
	old := watchDebounce
	watchDebounce = 20 * time.Millisecond
	t.Cleanup(func() { watchDebounce = old })
}

func newWatchEnv(t *testing.T, name, content string) *watchEnv {
	t.Helper()
	shortDebounce(t)
	e := &watchEnv{
		path:  filepath.Join(t.TempDir(), name),
		loads: make(chan struct{}, 64),
		errs:  make(chan error, 64),
	}
	if err := os.WriteFile(e.path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	e.c = New()
	e.c.SetConfigFile(e.path)
	if err := e.c.BindRef("", &e.cfg); err != nil {
		t.Fatal(err)
	}
	e.c.OnConfigLoad(func() { e.nloads.Add(1); e.loads <- struct{}{} })
	e.c.OnConfigError(func(err error) { e.errs <- err })
	if err := e.c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	// ReadConfig fires the load hook too; drain it.
	<-e.loads
	e.nloads.Store(0)
	if err := e.c.WatchConfig(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.c.StopWatch)
	return e
}

func (e *watchEnv) waitLoad(t *testing.T) {
	t.Helper()
	select {
	case <-e.loads:
	case err := <-e.errs:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for reload")
	}
}

func (e *watchEnv) waitErr(t *testing.T) error {
	t.Helper()
	select {
	case err := <-e.errs:
		return err
	case <-e.loads:
		t.Fatal("unexpected reload")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for error")
	}
	return nil
}

func (e *watchEnv) expectQuiet(t *testing.T) {
	t.Helper()
	select {
	case <-e.loads:
		t.Fatal("unexpected reload")
	case err := <-e.errs:
		t.Fatalf("unexpected error: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWatchOverwrite(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	if err := os.WriteFile(e.path, []byte(`{"host":"b","port":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitLoad(t)
	var host string
	var port int
	e.c.View(func() { host, port = e.cfg.Host, e.cfg.Port })
	if host != "b" || port != 2 {
		t.Fatalf("got %s %d", host, port)
	}
	if e.c.GetString("host") != "b" || e.c.GetInt("port") != 2 {
		t.Fatalf("Get not updated: %v", e.c.Get(""))
	}
}

func TestWatchAtomicRename(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	tmp := filepath.Join(filepath.Dir(e.path), "c.json.tmp")
	if err := os.WriteFile(tmp, []byte(`{"host":"renamed","port":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, e.path); err != nil {
		t.Fatal(err)
	}
	e.waitLoad(t)
	if got := e.c.GetString("host"); got != "renamed" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchRemoveAndRecreate(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	if err := os.Remove(e.path); err != nil {
		t.Fatal(err)
	}
	e.expectQuiet(t)
	if got := e.c.GetString("host"); got != "a" {
		t.Fatalf("value lost: %q", got)
	}
	if err := os.WriteFile(e.path, []byte(`{"host":"again","port":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitLoad(t)
	if got := e.c.GetString("host"); got != "again" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchBadContentThenRecover(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	if err := os.WriteFile(e.path, []byte(`{"host":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.waitErr(t); err == nil {
		t.Fatal("nil error")
	}
	if got := e.c.GetString("host"); got != "a" {
		t.Fatalf("old value not kept: %q", got)
	}
	if err := os.WriteFile(e.path, []byte(`{"host":"ok","port":5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitLoad(t)
	if got := e.c.GetString("host"); got != "ok" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchIgnoresOwnWrite(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	if err := e.c.Set("host", "written"); err != nil {
		t.Fatal(err)
	}
	if err := e.c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	e.expectQuiet(t)
	if got := e.c.GetString("host"); got != "written" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchDebounce(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	for i := 1; i <= 5; i++ {
		content := []byte(`{"host":"h` + string(rune('0'+i)) + `","port":1}`)
		if err := os.WriteFile(e.path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.waitLoad(t)
	e.expectQuiet(t)
	if n := e.nloads.Load(); n > 2 {
		t.Fatalf("%d reloads", n+0)
	}
	if got := e.c.GetString("host"); got != "h5" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchSymlinkSwap(t *testing.T) {
	shortDebounce(t)
	dir := t.TempDir()
	mk := func(v, host string) {
		if err := os.MkdirAll(filepath.Join(dir, v), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, v, "config.json"), []byte(`{"host":"`+host+`","port":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("v1", "one")
	if err := os.Symlink("v1", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "config.json"), filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	c := New()
	c.SetConfigFile(filepath.Join(dir, "config.json"))
	var cfg server
	if err := c.BindRef("", &cfg); err != nil {
		t.Fatal(err)
	}
	loads := make(chan struct{}, 8)
	c.OnConfigLoad(func() { loads <- struct{}{} })
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	<-loads
	if err := c.WatchConfig(); err != nil {
		t.Fatal(err)
	}
	defer c.StopWatch()

	mk("v2", "two")
	if err := os.Symlink("v2", filepath.Join(dir, "..data_tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-loads:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	if got := c.GetString("host"); got != "two" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchStopAndRestart(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	e.c.StopWatch()
	e.c.StopWatch()
	if err := os.WriteFile(e.path, []byte(`{"host":"b","port":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectQuiet(t)
	if got := e.c.GetString("host"); got != "a" {
		t.Fatalf("got %q", got)
	}
	if err := e.c.WatchConfig(); err != nil {
		t.Fatal(err)
	}
	defer e.c.StopWatch()
	if err := os.WriteFile(e.path, []byte(`{"host":"c","port":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitLoad(t)
	if got := e.c.GetString("host"); got != "c" {
		t.Fatalf("got %q", got)
	}
}

func TestWatchErrorsAndIdempotent(t *testing.T) {
	c := New()
	defer c.StopWatch()
	if _, ok := c.WatchConfig().(NoConfigFileError); !ok {
		t.Fatal("want NoConfigFileError")
	}
	c.SetConfigFile(filepath.Join(t.TempDir(), "missing-dir", "c.json"))
	if err := c.WatchConfig(); err == nil {
		t.Fatal("want error for missing directory")
	}
	c.SetConfigFile(writeTemp(t, "c.json", `{}`))
	if err := c.WatchConfig(); err != nil {
		t.Fatal(err)
	}
	if err := c.WatchConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestWatchSubSharesWatcher(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	sub := e.c.Sub("x")
	if err := sub.WatchConfig(); err != nil {
		t.Fatal(err)
	}
	e.c.StopWatch()
	e.c.s.mu.RLock()
	w := e.c.s.watcher
	e.c.s.mu.RUnlock()
	if w != nil {
		t.Fatal("watcher still set")
	}
	if err := os.WriteFile(e.path, []byte(`{"host":"b","port":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expectQuiet(t)
}

func TestWatchNoLogByDefault(t *testing.T) {
	e := newWatchEnv(t, "c.json", `{"host":"a","port":1}`)
	e.c.s.mu.RLock()
	l := e.c.s.logger
	e.c.s.mu.RUnlock()
	if l != nil {
		t.Fatal("logger should be nil by default")
	}
	if err := os.WriteFile(e.path, []byte(`{"host":`), 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitErr(t)
}
