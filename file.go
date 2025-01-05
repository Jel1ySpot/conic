package conic

import (
    "fmt"
    "github.com/fsnotify/fsnotify"
    "os"
    "path"
    "path/filepath"
    "strings"
    "sync"
)

type Config interface {
    Type() string
    Read() ([]byte, error)
    Write(b []byte) error
    OnChanged(cb func())
}

type RegularFile struct {
    FileName string
}

func (f RegularFile) Type() string {
    ext := strings.ToLower(filepath.Ext(f.FileName))

    if len(ext) > 1 {
        return ext[1:]
    }

    return ""
}

func (f RegularFile) Read() ([]byte, error) {
    if f.FileName == "" {
        return nil, NoConfigFileError{}
    }

    file, err := os.ReadFile(f.FileName)
    if err != nil {
        return nil, ConfigFileReadError{err}
    }

    return file, nil
}

func (f RegularFile) Write(b []byte) error {
    if f.FileName == "" {
        return NoConfigFileError{}
    }

    if dir := path.Dir(f.FileName); dir != "." {
        err := os.MkdirAll(path.Dir(f.FileName), 0755)
        if err != nil {
            return err
        }
    }
    return os.WriteFile(f.FileName, b, 0644)
}

func (f RegularFile) OnChanged(cb func()) {
    initWG := sync.WaitGroup{}
    initWG.Add(1)
    go func() {
        watcher, err := fsnotify.NewWatcher()
        if err != nil {
            c.logger(fmt.Sprintf("failed to create watcher: %s", err))
            os.Exit(1)
        }
        defer watcher.Close()

        eventsWG := sync.WaitGroup{}
        eventsWG.Add(1)
        go func() {
            for {
                select {
                case event, ok := <-watcher.Events:
                    if !ok { // 'Events' channel is closed
                        eventsWG.Done()
                        return
                    }

                    if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
                        cb()
                    } else if event.Has(fsnotify.Remove) {
                        eventsWG.Done()
                        return
                    }

                case err, ok := <-watcher.Errors:
                    if ok { // 'Errors' channel is not closed
                        c.logger(fmt.Sprintf("watcher error: %s", err))
                    }
                    eventsWG.Done()
                    return
                }
            }
        }()
        watcher.Add(f.FileName)
        initWG.Done()   // done initializing the watch in this go routine, so the parent routine can move on...
        eventsWG.Wait() // now, wait for event loop to end in this go-routine...
    }()
    initWG.Wait() // make sure that the go routine above fully ended before returning
}
