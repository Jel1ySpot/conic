> This project is in early developing. We need your help to make it perfect
# conic
___
> Another easy way for configuration

## Install
```shell
go get -u github.com/Jel1ySpot/conic
```

## How conic works?
By binding variable and configuration, you can access and change the configuration in a easy way.
> Configuration (usually a file) <=> Go Variable

Struct fields are named with `mapstructure` tags (`json` / `yaml` tags are ignored).

## Getting start
### Quick start
```go
package main

import (
    "errors"
    "fmt"
    "io/fs"
    "log"

    "github.com/Jel1ySpot/conic"
)

type Config struct {
    Name   string `mapstructure:"name" default:"alex"`
    Age    int    `mapstructure:"age" default:"21"`
    Server struct {
        Host string `mapstructure:"host" default:"localhost"`
        Port int    `mapstructure:"port" default:"8080" env:"APP_PORT"`
    } `mapstructure:"server"`
}

func main() {
    var cfg Config

    conic.SetConfigFile("config.yaml")

    // Binding. cfg is filled from defaults and environment right away.
    if err := conic.BindRef("", &cfg); err != nil {
        log.Fatal(err)
    }

    // Read configuration file. A missing file is fine: keep the defaults.
    if err := conic.ReadConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
        log.Fatal(err)
    }

    // Reload automatically when the file changes.
    if err := conic.WatchConfig(); err != nil {
        log.Fatal(err)
    }
    defer conic.StopWatch()

    fmt.Printf("%+v\n", cfg)

    // Change value
    conic.Update(func() {
        cfg.Name = "Richard"
        cfg.Age += 7
    })

    // Creates the file (and its parent directories) if it does not exist.
    if err := conic.WriteConfig(); err != nil {
        log.Fatal(err)
    }
    /* config.yaml
       name: Richard
       age: 28
       server:
         host: localhost
         port: 8080
    */
}
```

### Formats
`json`, `yaml` and `yml` are supported. The format is inferred from the extension passed to `SetConfigFile`.
Use `SetConfigType` to override it (e.g. for a file without extension), or `UseAdapter` to plug in your own
codec (any value with `Encode(v any) ([]byte, error)` and `Decode(b []byte, v any) error`).

```go
conic.SetConfigFile("/etc/myapp/config")
if err := conic.SetConfigType("yaml"); err != nil { // UnsupportedConfigError for unknown types
    log.Fatal(err)
}
```

## Data layers
A value is resolved from the following layers, later ones win:

1. initial value of the bound struct
2. `default` tag
3. configuration file
4. environment variable (`env` tag)

- `WriteConfig` never writes values that come from environment variables into the file.
- Keys in the file that are not defined by any bound struct are kept on write and can be read with `Get`.
- `ReadConfig` is all-or-nothing: if parsing or decoding fails, nothing changes.

## Accessing values
```go
conic.Get("server.port")       // any, nil if absent
conic.GetString("server.host") // "" if absent
conic.GetInt("server.port")    // 0 if absent or not convertible
conic.IsSet("server.port")     // file or environment only, defaults do not count

err := conic.Set("server.port", 9090) // updates bound structs too
```

- Keys are separated by `.` and case-sensitive. A field without a `mapstructure` tag uses the field name as-is as its key (e.g. `Name`); tagging every field is recommended.
- `Get` returns the merged value (defaults < file < env) as a copy.
- `Set` returns a `BindError` and changes nothing if the value cannot be decoded into the bound structs.
  Environment variables have the highest priority, so `Set` does not override a value that comes from one.

### Sub
`Sub` returns a view that shares the same state, with keys relative to a prefix.

```go
server := conic.Sub("server")
server.GetInt("port")            // same as conic.GetInt("server.port")
server.BindRef("", &serverCfg)   // bind just this section
```

## Hot reload
```go
conic.OnConfigLoad(func() { log.Println("config loaded") })
conic.OnConfigError(func(err error) { log.Println("reload failed:", err) })

if err := conic.WatchConfig(); err != nil {
    log.Fatal(err)
}
defer conic.StopWatch()
```

- `WatchConfig` watches the directory of the config file, so editors that save atomically and Kubernetes
  ConfigMap symlink swaps are both picked up.
- Events are debounced, and the changes made by `WriteConfig` itself do not trigger a reload.
- `OnConfigLoad` runs after every successful load, including manual `ReadConfig` calls and reloads.
- `OnConfigError` runs when an automatic reload fails; the previous values stay in effect.
  Manual `ReadConfig` calls return their error instead.
- `StopWatch` stops watching.

## Concurrency
Bound structs are modified by conic during reloads. Use `View` / `Update` to access them safely.

```go
conic.View(func() {
    fmt.Println(cfg.Name)
})

conic.Update(func() {
    cfg.Name = "Richard"
})
```

`fn` runs while conic's lock is held, so it must not call any conic method.

## Errors
| Error                    | Meaning                                                                    |
|--------------------------|----------------------------------------------------------------------------|
| `NoConfigFileError`      | No config file path is set                                                 |
| `UnsupportedConfigError` | Unknown config type, or no adapter to handle it                            |
| `ConfigFileNotFoundError`| The config file does not exist (`errors.Is(err, fs.ErrNotExist)` is true)  |
| `ConfigFileReadError`    | Reading the config file failed                                             |
| `ConfigParseError`       | The file content could not be parsed                                       |
| `ConfigMarshalError`     | Encoding the configuration failed                                          |
| `ConfigFileWriteError`   | Writing the config file (or creating its directory) failed                 |
| `BindError`              | Binding or decoding into a Go value failed (carries the key)               |

All of them work with `errors.As`; those that wrap an underlying error also support `errors.Is` through `Unwrap`.

## Logging
Nothing is logged by default. Enable it with `SetLogger`:

```go
conic.SetLogger(log.Printf)
```

## Instances
Every method has a package-level function of the same name that operates on a default instance.
Use `conic.New()` to create an independent one.

```go
c := conic.New()
c.SetConfigFile("other.json")
err := c.BindRef("", &other)
```

## Limitations
- Writing back loses comments and key order.
- Slices are replaced as a whole; paths cannot index into a slice.
- Removing a key from a bound map does not remove it from the file. Only zero-valued `omitempty` fields
  delete their key.
