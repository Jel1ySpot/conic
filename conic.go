package conic

import (
    "fmt"
    "github.com/Jel1ySpot/conic/internal/adapter"
    jsonAdapter "github.com/Jel1ySpot/conic/internal/adapter/json"
    "os"
    "path/filepath"
    "strings"
)

// UnsupportedConfigError denotes encountering an unsupported
// configuration filetype.
type UnsupportedConfigError string

// Error returns the formatted configuration error.
func (str UnsupportedConfigError) Error() string {
    return fmt.Sprintf("Unsupported Config Type %q", string(str))
}

// NoConfigFileError denotes failing when file path empty.
type NoConfigFileError struct{}

// Error returns the formatted configuration error.
func (fnfe NoConfigFileError) Error() string {
    return fmt.Sprintf("No Config File")
}

// ConfigFileReadError denotes failing when reading file.
type ConfigFileReadError struct {
    err error
}

// Error returns the formatted configuration error.
func (cfre ConfigFileReadError) Error() string {
    return fmt.Sprintf("Reading Config File Failed: %v", cfre.err)
}

// ConfigFileAlreadyExistsError denotes failure to write new configuration file.
type ConfigFileAlreadyExistsError string

// Error returns the formatted error when configuration already exists.
func (faee ConfigFileAlreadyExistsError) Error() string {
    return fmt.Sprintf("Config File %q Already Exists", string(faee))
}

// ConfigMarshalError happens when failing to marshal the configuration.
type ConfigMarshalError struct {
    err error
}

// Error returns the formatted configuration error.
func (e ConfigMarshalError) Error() string {
    return fmt.Sprintf("While marshaling config: %s", e.err.Error())
}

var c *Conic

func init() {
    c = New()
}

type Conic struct {
    keyDelim string

    logger func(format string, args ...interface{})

    configFile        string
    configType        string
    configPermissions os.FileMode

    bindStructs []struct {
        path []string
        ref  any
    }

    parents        []string
    config         map[string]any
    override       map[string]any
    defaults       map[string]any
    kvstore        map[string]any
    aliases        map[string]string
    typeByDefValue bool

    onConfigLoad []func()

    adapter adapter.Adapter
}

// New returns an initialized Conic instance.
func New() *Conic {
    c := new(Conic)
    c.keyDelim = "."
    c.logger = func(format string, args ...interface{}) {
        fmt.Printf(format+"\n", args...)
    }
    c.configPermissions = os.ModePerm
    c.config = make(map[string]any)
    c.parents = []string{}
    c.override = make(map[string]any)
    c.defaults = make(map[string]any)
    c.kvstore = make(map[string]any)
    c.aliases = make(map[string]string)
    c.typeByDefValue = false

    return c
}

func SetLogger(logger func(format string, args ...interface{})) {
    c.SetLogger(logger)
}

func (c *Conic) SetLogger(logger func(format string, args ...interface{})) {
    c.logger = logger
}

// SetConfigFile explicitly defines the path, name and extension of the config file.
// Conic will use this and not check any of the config paths.
func SetConfigFile(in string) { c.SetConfigFile(in) }

func (c *Conic) SetConfigFile(in string) {
    if in != "" {
        c.configFile = in
    }
}

func (c *Conic) setConfigAdapter() error {
    switch c.getConfigType() {
    case "json":
        c.adapter = jsonAdapter.Adapter{}
    default:
        return UnsupportedConfigError(c.getConfigType())
    }
    return nil
}

// SetConfigType sets the type of the configuration
func SetConfigType(ext string) error { return c.SetConfigType(ext) }

func (c *Conic) SetConfigType(ext string) error {
    c.configType = ext
    if err := c.setConfigAdapter(); err != nil {
        c.configType = ""
        return err
    }
    return nil
}

func (c *Conic) getConfigType() string {
    if c.configType != "" {
        return c.configType
    }

    ext := strings.ToLower(filepath.Ext(c.configFile))

    if len(ext) > 1 {
        return ext[1:]
    }

    return ""
}

func searchMap(source map[string]any, path []string) map[string]any {
    if len(path) == 0 {
        return source
    }

    next, ok := source[path[0]]
    if ok {
        switch next := next.(type) {
        case map[string]any:
            if next == nil {
                source[path[0]] = make(map[string]any)
            }
            if len(path) == 1 {
                return next
            }
            return searchMap(next, path[1:])
        default:
            return nil
        }
    } else if len(path) == 1 {
        source[path[0]] = make(map[string]any)
        return source[path[0]].(map[string]any)
    }
    return nil
}

func GetConic() *Conic {
    return c
}

func BindRef(key string, ref any) { c.BindRef(key, ref) }

func (c *Conic) BindRef(key string, ref any) {
    c.bindStructs = append(c.bindStructs, struct {
        path []string
        ref  any
    }{path: strings.Split(key, c.keyDelim), ref: ref})
}

func (c *Conic) marshalAll() error {
    for _, s := range c.bindStructs {
        data := searchMap(c.config, s.path)
        b, err := c.adapter.Encode(s.ref)
        if err != nil {
            return err
        }
        if err := c.adapter.Decode(b, &data); err != nil {
            return err
        }
    }
    return nil
}

func unmarshalAll() error { return c.unmarshalAll() }

func (c *Conic) unmarshalAll() error {
    for _, s := range c.bindStructs {
        data := searchMap(c.config, s.path)
        if data == nil {
            continue
        }
        b, err := c.adapter.Encode(data)
        if err != nil {
            return err
        }
        if err := c.adapter.Decode(b, s.ref); err != nil {
            return err
        }
    }
    return nil
}
