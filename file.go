package conic

import (
    "fmt"
    "github.com/fsnotify/fsnotify"
    "os"
    "sync"
)

// ReadInConfig loads the configuration file
func ReadInConfig() error { return c.ReadInConfig() }

func (c *Conic) ReadInConfig() error {
    c.logger("attempting to read in config file")
    if c.configFile == "" {
        return NoConfigFileError{}
    }

    if err := c.UseAdapter(); err != nil {
        return err
    }

    file, err := os.ReadFile(c.configFile)
    if err != nil {
        return ConfigFileReadError{err}
    }

    var config map[string]any

    err = c.adapter.Decode(file, &config)
    if config == nil || err != nil {
        return ConfigFileReadError{err}
    }

    c.config = config

    defer func() {
        if len(c.onConfigLoad) > 0 {
            for _, f := range c.onConfigLoad {
                go f()
            }
        }
    }()

    return unmarshalAll()
}

// WriteConfig writes config in the configuration file
func WriteConfig() error { return c.WriteConfig() }

func (c *Conic) WriteConfig() error {
    if err := c.marshalAll(); err != nil {
        return err
    }

    b, err := c.adapter.Encode(c.config)
    if err != nil {
        return err
    }

    if err := os.WriteFile(c.configFile, b, os.ModePerm); err != nil {
        return err
    }

    return nil
}

// WatchConfig starts watching a config file for changes.
func WatchConfig() { c.WatchConfig() }

// WatchConfig starts watching a config file for changes.
func (c *Conic) WatchConfig() {
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
                        if err := c.ReadInConfig(); err != nil {
                            c.logger(fmt.Sprintf("read config file: %s", err))
                        }
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
        watcher.Add(c.configFile)
        initWG.Done()   // done initializing the watch in this go routine, so the parent routine can move on...
        eventsWG.Wait() // now, wait for event loop to end in this go-routine...
    }()
    initWG.Wait() // make sure that the go routine above fully ended before returning
}
