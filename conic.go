package conic

import (
	"fmt"
	"github.com/Jel1ySpot/conic/internal/adapter"
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

	configInput Config
	configType  string

	bindStructs []struct {
		path []string
		ref  any
	}

	parent     *Conic
	parentPath []string

	config map[string]any

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
	c.configInput = RegularFile{}
	c.config = make(map[string]any)
	c.parentPath = []string{}

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
		c.configInput = RegularFile{in}
		_ = c.SetConfigType(c.configInput.Type())
	}
}

// UseAdapter uses adapter for loading config
func UseAdapter(a adapter.Adapter) { c.UseAdapter(a) }

func (c *Conic) UseAdapter(a adapter.Adapter) { c.adapter = a }

// SetConfigType sets the type of the configuration
func SetConfigType(ext string) error { return c.SetConfigType(ext) }

func (c *Conic) SetConfigType(ext string) error {
	if ext == "" {
		ext = c.getConfigType()
	}
	c.configType = ext
	switch ext {
	case "json":
		c.UseAdapter(adapter.Json{})
	case "yaml":
		c.UseAdapter(adapter.Yaml{})
	default:
		return UnsupportedConfigError(ext)
	}
	return nil
}

func (c *Conic) getConfigType() string {
	if c.parent != nil {
		return c.parent.getConfigType()
	}
	if c.configType != "" {
		return c.configType
	}

	if c.configInput != nil {
		return c.configInput.Type()
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

// ReadConfig loads the configuration file
func ReadConfig() error { return c.ReadConfig() }

func (c *Conic) ReadConfig() error {
	if c.parent != nil {
		return c.parent.ReadConfig()
	}
	c.logger("attempting to read in config file")

	file, err := c.configInput.Read()
	if err != nil {
		return err
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

	return c.unmarshalAll()
}

// WriteConfig writes config in the configuration file
func WriteConfig() error { return c.WriteConfig() }

func (c *Conic) WriteConfig() error {
	if c.parent != nil {
		return c.parent.WriteConfig()
	}
	if err := c.marshalAll(); err != nil {
		return err
	}

	b, err := c.adapter.Encode(c.config)
	if err != nil {
		return err
	}

	if err := c.configInput.Write(b); err != nil {
		return err
	}

	return nil
}

// WatchConfig starts watching a config file for changes.
func WatchConfig() { c.WatchConfig() }

// WatchConfig starts watching a config file for changes.
func (c *Conic) WatchConfig() {
	if c.parent != nil {
		c.parent.WatchConfig()
		return
	}
	c.configInput.OnChanged(func() {
		if err := c.ReadConfig(); err != nil {
			c.logger(fmt.Sprintf("read config file: %s", err))
		}
	})
}

func GetConic() *Conic {
	return c
}

func BindRef(key string, ref any) { c.BindRef(key, ref) }

func (c *Conic) BindRef(key string, ref any) {
	var path []string
	if key != "" {
		path = strings.Split(key, c.keyDelim)
	}
	c.bindStructs = append(c.bindStructs, struct {
		path []string
		ref  any
	}{path: path, ref: ref})
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

type SubConic struct {
	*Conic
}

func (c SubConic) Type() string {
	return c.getConfigType()
}

func (c *Conic) Sub(key string) *Conic {
	var path []string
	if key != "" {
		path = strings.Split(key, c.keyDelim)
	}
	newConfig := make(map[string]any)
	c.BindRef(key, &newConfig)
	defer func(c *Conic) {
		err := c.unmarshalAll()
		if err != nil {
			c.logger(fmt.Sprintf("config unmarshal error: %s", err))
		}
	}(c)
	if key == "" {
		return &Conic{
			keyDelim:    c.keyDelim,
			logger:      c.logger,
			configInput: c.configInput,
			configType:  c.configType,
			parent:      c,
			parentPath:  c.parentPath,
			config:      newConfig,
			adapter:     c.adapter,
		}
	}

	return &Conic{
		keyDelim:   c.keyDelim,
		logger:     c.logger,
		configType: c.configType,
		parent:     c,
		parentPath: append(c.parentPath, path...),
		config:     newConfig,
		adapter:    c.adapter,
	}
}
