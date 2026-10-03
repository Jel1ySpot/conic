package conic

import "fmt"

// NoConfigFileError denotes failing when no config file path is set.
type NoConfigFileError struct{}

// Error returns the formatted configuration error.
func (NoConfigFileError) Error() string {
	return "conic: no config file set"
}

// UnsupportedConfigError denotes encountering an unsupported
// configuration filetype (or having no adapter to handle it).
type UnsupportedConfigError string

// Error returns the formatted configuration error.
func (e UnsupportedConfigError) Error() string {
	return fmt.Sprintf("conic: unsupported config type %q", string(e))
}

// ConfigFileNotFoundError denotes that the config file does not exist.
// It unwraps to the underlying error, so errors.Is(err, fs.ErrNotExist) holds.
type ConfigFileNotFoundError struct {
	Path string
	Err  error
}

func (e ConfigFileNotFoundError) Error() string {
	return fmt.Sprintf("conic: config file %q not found: %v", e.Path, e.Err)
}

func (e ConfigFileNotFoundError) Unwrap() error { return e.Err }

// ConfigFileReadError denotes failing when reading the config file.
type ConfigFileReadError struct {
	Path string
	Err  error
}

func (e ConfigFileReadError) Error() string {
	return fmt.Sprintf("conic: reading config file %q failed: %v", e.Path, e.Err)
}

func (e ConfigFileReadError) Unwrap() error { return e.Err }

// ConfigParseError denotes failing to parse the config file content.
type ConfigParseError struct {
	Path string
	Err  error
}

func (e ConfigParseError) Error() string {
	return fmt.Sprintf("conic: parsing config file %q failed: %v", e.Path, e.Err)
}

func (e ConfigParseError) Unwrap() error { return e.Err }

// ConfigMarshalError happens when failing to marshal the configuration.
type ConfigMarshalError struct {
	Err error
}

func (e ConfigMarshalError) Error() string {
	return fmt.Sprintf("conic: marshaling config failed: %v", e.Err)
}

func (e ConfigMarshalError) Unwrap() error { return e.Err }

// ConfigFileWriteError denotes failing when writing the config file.
type ConfigFileWriteError struct {
	Path string
	Err  error
}

func (e ConfigFileWriteError) Error() string {
	return fmt.Sprintf("conic: writing config file %q failed: %v", e.Path, e.Err)
}

func (e ConfigFileWriteError) Unwrap() error { return e.Err }

// BindError denotes a failure to bind or decode configuration into a Go value.
type BindError struct {
	Key string
	Err error
}

func (e BindError) Error() string {
	return fmt.Sprintf("conic: binding %q failed: %v", e.Key, e.Err)
}

func (e BindError) Unwrap() error { return e.Err }
