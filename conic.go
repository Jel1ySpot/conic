// Package conic binds Go structs to configuration files, with defaults,
// environment overrides and typed access.
package conic

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/Jel1ySpot/conic/internal/adapter"
	"github.com/go-viper/mapstructure/v2"
)

var std = New()

// GetConic returns the default instance used by the package-level functions.
func GetConic() *Conic { return std }

// adapters maps config types (file extensions) to adapters.
var adapters = map[string]adapter.Adapter{
	"json": adapter.Json{},
	"yaml": adapter.Yaml{},
	"yml":  adapter.Yaml{},
}

type binding struct {
	absPath  []string
	ref      any
	typ      reflect.Type
	base     any
	defaults []tagEntry
	envs     []tagEntry
}

// state is the shared state behind a Conic and all of its Sub views.
type state struct {
	mu sync.RWMutex

	keyDelim string
	logger   func(format string, args ...any)

	configFile      string
	configType      string
	adapter         adapter.Adapter
	adapterExplicit bool

	data     map[string]any
	bindings []*binding

	// Caches rebuilt by rebuildLayersLocked whenever the bindings change.
	defaultsTree map[string]any // never modified after construction
	defaults     *flatLayer
	envs         *flatLayer

	loadHooks  []func()
	errorHooks []func(error)

	lastHash [32]byte

	watcher *watcher
}

// Conic is a view onto a configuration state, rooted at a key prefix.
type Conic struct {
	s      *state
	prefix []string
}

// New returns an initialized, independent Conic instance.
func New() *Conic {
	s := &state{
		keyDelim: ".",
		data:     map[string]any{},
	}
	s.rebuildLayersLocked()
	return &Conic{s: s}
}

func (c *Conic) path(key string) []string {
	return joinPath(c.prefix, splitKey(key, c.s.keyDelim)...)
}

func (s *state) joinKey(path []string) string { return strings.Join(path, s.keyDelim) }

// Sub returns a view whose keys are relative to key. It shares all state with
// the receiver.
func (c *Conic) Sub(key string) *Conic {
	return &Conic{s: c.s, prefix: c.path(key)}
}

// Sub is Conic.Sub on the default instance.
func Sub(key string) *Conic { return std.Sub(key) }

// ---- logging ----------------------------------------------------------

// SetLogger sets the logger. The default is nil: nothing is logged.
func (c *Conic) SetLogger(logger func(format string, args ...any)) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.logger = logger
}

// SetLogger is Conic.SetLogger on the default instance.
func SetLogger(logger func(format string, args ...any)) { std.SetLogger(logger) }

// logf must not be called while holding s.mu.
func (s *state) logf(format string, args ...any) {
	s.mu.RLock()
	l := s.logger
	s.mu.RUnlock()
	if l != nil {
		l(format, args...)
	}
}

// ---- file / adapter configuration --------------------------------------

// SetConfigFile sets the config file path. Unless an adapter or config type
// was set explicitly, the adapter is inferred from the extension.
func (c *Conic) SetConfigFile(path string) {
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configFile = path
	if path == "" || s.adapterExplicit {
		return
	}
	s.configType = extOf(path)
	s.adapter = adapters[s.configType] // nil if unknown; reported on Read/Write
}

// SetConfigFile is Conic.SetConfigFile on the default instance.
func SetConfigFile(path string) { std.SetConfigFile(path) }

// SetConfigType explicitly sets the config type ("json", "yaml", "yml").
func (c *Conic) SetConfigType(ext string) error {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	a, ok := adapters[ext]
	if !ok {
		return UnsupportedConfigError(ext)
	}
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configType = ext
	s.adapter = a
	s.adapterExplicit = true
	return nil
}

// SetConfigType is Conic.SetConfigType on the default instance.
func SetConfigType(ext string) error { return std.SetConfigType(ext) }

// UseAdapter sets a custom adapter, taking precedence over the file extension.
func (c *Conic) UseAdapter(a adapter.Adapter) {
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapter = a
	s.adapterExplicit = true
}

// UseAdapter is Conic.UseAdapter on the default instance.
func UseAdapter(a adapter.Adapter) { std.UseAdapter(a) }

// prepare returns the file path and adapter, or the matching error.
func (s *state) prepare() (string, adapter.Adapter, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prepareLocked()
}

func (s *state) prepareLocked() (string, adapter.Adapter, error) {
	if s.configFile == "" {
		return "", nil, NoConfigFileError{}
	}
	if s.adapter == nil {
		return "", nil, UnsupportedConfigError(s.configType)
	}
	return s.configFile, s.adapter, nil
}

// ---- layers ------------------------------------------------------------

func (s *state) envTreeLocked() map[string]any {
	t := map[string]any{}
	for _, b := range s.bindings {
		for _, e := range b.envs {
			if v, ok := os.LookupEnv(e.val); ok {
				if p := joinPath(b.absPath, e.path...); len(p) > 0 {
					setPath(t, p, v)
				}
			}
		}
	}
	return t
}

func (s *state) mergedLocked() map[string]any {
	out := map[string]any{}
	mergeMaps(out, s.defaultsTree)
	mergeMaps(out, s.data)
	mergeMaps(out, s.envTreeLocked())
	return out
}

// ---- decoding / syncing -------------------------------------------------

func decodeBinding(b *binding, merged map[string]any) (reflect.Value, error) {
	var in any = merged
	if len(b.absPath) > 0 {
		in, _ = lookup(merged, b.absPath)
	}
	nv := reflect.New(b.typ)
	if b.typ.Kind() == reflect.Struct && !isLeafStruct(b.typ) {
		// Start from the current value so that fields mapstructure ignores
		// are preserved; every mapped field is reset first.
		nv.Elem().Set(reflect.ValueOf(b.ref).Elem())
		zeroMapped(nv.Elem())
	}
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:          "mapstructure",
		WeaklyTypedInput: true,
		MatchName:        func(mapKey, fieldName string) bool { return mapKey == fieldName },
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
			mapstructure.TextUnmarshallerHookFunc(),
		),
		Result: nv.Interface(),
	})
	if err != nil {
		return reflect.Value{}, err
	}
	if err := dec.Decode(in); err != nil {
		return reflect.Value{}, err
	}
	return nv, nil
}

// decodeAllLocked decodes every binding from the merged view and, only if all
// succeed, commits the results into the bound values.
func (s *state) decodeAllLocked() error {
	merged := s.mergedLocked()
	vals := make([]reflect.Value, len(s.bindings))
	for i, b := range s.bindings {
		nv, err := decodeBinding(b, merged)
		if err != nil {
			return BindError{Key: s.joinKey(b.absPath), Err: err}
		}
		vals[i] = nv
	}
	for i, b := range s.bindings {
		reflect.ValueOf(b.ref).Elem().Set(vals[i].Elem())
	}
	return nil
}

// syncLocked writes the bound values into the file layer. In full mode every
// value is written. Otherwise only leaves already present in the file layer or
// differing from the current merged view are written, so defaults stay
// defaults (IsSet stays false).
func (s *state) syncLocked(full bool) {
	var merged map[string]any
	if !full {
		merged = s.mergedLocked()
	}
	for _, b := range s.bindings {
		col := &collector{}
		plain, ok := toPlain(reflect.ValueOf(b.ref).Elem(), col)
		if !ok {
			continue
		}
		if pm, isMap := plain.(map[string]any); isMap {
			for _, e := range b.envs {
				if _, set := os.LookupEnv(e.val); set {
					deleteAt(pm, e.path)
				}
			}
		}
		for _, p := range col.omitted {
			deleteAt(s.data, joinPath(b.absPath, p...))
		}
		if full {
			mergeAt(s.data, b.absPath, plain)
		} else {
			s.syncIncremental(b.absPath, plain, merged)
		}
	}
}

func (s *state) syncIncremental(path []string, plain any, merged map[string]any) {
	if pm, ok := plain.(map[string]any); ok && len(pm) > 0 {
		for k, v := range pm {
			s.syncIncremental(joinPath(path, k), v, merged)
		}
		return
	}
	if len(path) == 0 {
		return
	}
	_, inData := lookup(s.data, path)
	cur, _ := lookup(merged, path)
	if inData || fmt.Sprint(cur) != fmt.Sprint(plain) {
		mergeAt(s.data, path, plain)
	}
}

// ---- binding -------------------------------------------------------------

// BindRef binds the pointer ref to the config at key. The value is filled
// immediately from defaults, tags and environment, and again on every
// ReadConfig/Set. ref's mapstructure tags (not json/yaml tags) name the keys.
func (c *Conic) BindRef(key string, ref any) error {
	abs := c.path(key)
	s := c.s
	v := reflect.ValueOf(ref)
	if !v.IsValid() || v.Kind() != reflect.Ptr || v.IsNil() {
		return BindError{Key: s.joinKey(abs), Err: errors.New("ref must be a non-nil pointer")}
	}
	typ := v.Type().Elem()
	base, _ := toPlain(v.Elem(), nil)
	defaults, envs := collectTags(typ)
	b := &binding{absPath: abs, ref: ref, typ: typ, base: base, defaults: defaults, envs: envs}

	s.mu.Lock()
	defer s.mu.Unlock()
	oldTree, oldDefaults, oldEnvs := s.defaultsTree, s.defaults, s.envs
	s.bindings = append(s.bindings, b)
	s.rebuildLayersLocked()
	nv, err := decodeBinding(b, s.mergedLocked())
	if err != nil {
		s.bindings = s.bindings[:len(s.bindings)-1]
		s.defaultsTree, s.defaults, s.envs = oldTree, oldDefaults, oldEnvs
		return BindError{Key: s.joinKey(abs), Err: err}
	}
	v.Elem().Set(nv.Elem())
	return nil
}

// BindRef is Conic.BindRef on the default instance.
func BindRef(key string, ref any) error { return std.BindRef(key, ref) }

// ---- reading / writing ---------------------------------------------------

// load parses b and installs it as the file layer, transactionally. It does
// not fire hooks.
func (s *state) load(b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, ad, err := s.prepareLocked()
	if err != nil {
		return err
	}
	data := map[string]any{}
	if len(bytes.TrimSpace(b)) > 0 {
		var raw map[string]any
		if err := ad.Decode(b, &raw); err != nil {
			return ConfigParseError{Path: path, Err: err}
		}
		if raw != nil {
			data = normalize(raw).(map[string]any)
			if hasNulKey(data) {
				return ConfigParseError{Path: path, Err: errNulKey}
			}
		}
	}
	old := s.data
	s.data = data
	if err := s.decodeAllLocked(); err != nil {
		s.data = old
		return err
	}
	s.lastHash = sha256.Sum256(b)
	return nil
}

func (s *state) fireLoad() {
	s.mu.RLock()
	hooks := append([]func(){}, s.loadHooks...)
	s.mu.RUnlock()
	for _, f := range hooks {
		f()
	}
}

func (s *state) fireError(err error) {
	s.mu.RLock()
	hooks := append([]func(error){}, s.errorHooks...)
	s.mu.RUnlock()
	for _, f := range hooks {
		f(err)
	}
}

// sameAsLast reports whether b equals the last content read or written.
func (s *state) sameAsLast(b []byte) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sha256.Sum256(b) == s.lastHash
}

// ReadConfig reads the config file and updates all bound values. It is
// all-or-nothing: on error nothing changes. OnConfigLoad hooks run afterwards.
func (c *Conic) ReadConfig() error {
	s := c.s
	path, _, err := s.prepare()
	if err != nil {
		return err
	}
	s.logf("conic: reading config file %q", path)
	b, err := readFile(path)
	if err != nil {
		return err
	}
	if err := s.load(b); err != nil {
		return err
	}
	s.fireLoad()
	return nil
}

// ReadConfig is Conic.ReadConfig on the default instance.
func ReadConfig() error { return std.ReadConfig() }

// WriteConfig synchronizes bound values into the file layer and writes the
// file. Keys not described by any bound struct are preserved, and values that
// come from environment variables are not written.
func (c *Conic) WriteConfig() error {
	s := c.s
	s.mu.Lock()
	path, ad, err := s.prepareLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.syncLocked(true)
	b, err := ad.Encode(s.data)
	if err != nil {
		s.mu.Unlock()
		return ConfigMarshalError{Err: err}
	}
	err = writeFile(path, b)
	if err == nil {
		s.lastHash = sha256.Sum256(b)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.logf("conic: wrote config file %q", path)
	return nil
}

// WriteConfig is Conic.WriteConfig on the default instance.
func WriteConfig() error { return std.WriteConfig() }

// ---- access --------------------------------------------------------------

// Get returns a copy of the merged value at key (defaults < file < env), or
// nil if absent. An empty key returns the whole tree (relative to a Sub prefix).
func (c *Conic) Get(key string) any {
	s := c.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.getLocked(c.path(key))
	if !ok {
		return nil
	}
	return v
}

// Get is Conic.Get on the default instance.
func Get(key string) any { return std.Get(key) }

// GetString returns the value at key as a string ("" if absent).
func (c *Conic) GetString(key string) string { return toString(c.Get(key)) }

// GetString is Conic.GetString on the default instance.
func GetString(key string) string { return std.GetString(key) }

// GetInt returns the value at key as an int (0 if absent or not convertible).
func (c *Conic) GetInt(key string) int { return toInt(c.Get(key)) }

// GetInt is Conic.GetInt on the default instance.
func GetInt(key string) int { return std.GetInt(key) }

// IsSet reports whether key is present in the file layer or set by an
// environment variable. Defaults do not count.
func (c *Conic) IsSet(key string) bool {
	s := c.s
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isSetLocked(c.path(key))
}

// IsSet is Conic.IsSet on the default instance.
func IsSet(key string) bool { return std.IsSet(key) }

// Set stores value at key in the file layer and refreshes all bound values.
// Unsaved modifications of bound values are kept. Environment variables have
// the highest priority, so Set does not override a value that comes from one.
// If the new data cannot be decoded into the bound values, nothing changes and
// a BindError is returned.
func (c *Conic) Set(key string, value any) error {
	s := c.s
	p := c.path(key)
	var plain any
	if value != nil {
		plain, _ = toPlain(reflect.ValueOf(value), nil)
	}
	if hasNulPath(p) || hasNulKey(plain) {
		return BindError{Key: s.joinKey(p), Err: errNulKey}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncLocked(false)
	old := deepCopy(s.data).(map[string]any)
	if len(p) == 0 {
		m, ok := plain.(map[string]any)
		if !ok {
			return BindError{Key: "", Err: errors.New("root value must be a map")}
		}
		s.data = m
	} else {
		setPath(s.data, p, plain)
	}
	if err := s.decodeAllLocked(); err != nil {
		s.data = old
		return err
	}
	return nil
}

// Set is Conic.Set on the default instance.
func Set(key string, value any) error { return std.Set(key, value) }

// View runs fn under a read lock, for safely reading bound values. fn must
// not call methods of conic (that would deadlock).
func (c *Conic) View(fn func()) {
	c.s.mu.RLock()
	defer c.s.mu.RUnlock()
	fn()
}

// View is Conic.View on the default instance.
func View(fn func()) { std.View(fn) }

// Update runs fn under the write lock, for safely modifying bound values;
// afterwards the values are synchronized so that Get sees them. fn must not
// call methods of conic (that would deadlock).
func (c *Conic) Update(fn func()) {
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
	s.syncLocked(false)
}

// Update is Conic.Update on the default instance.
func Update(fn func()) { std.Update(fn) }

// OnConfigLoad registers fn to run (synchronously, without locks held) after
// every successful ReadConfig, including reloads.
func (c *Conic) OnConfigLoad(fn func()) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.loadHooks = append(c.s.loadHooks, fn)
}

// OnConfigLoad is Conic.OnConfigLoad on the default instance.
func OnConfigLoad(fn func()) { std.OnConfigLoad(fn) }

// OnConfigError registers fn to run when an automatic reload fails. Manual
// ReadConfig calls return their error instead.
func (c *Conic) OnConfigError(fn func(error)) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.errorHooks = append(c.s.errorHooks, fn)
}

// OnConfigError is Conic.OnConfigError on the default instance.
func OnConfigError(fn func(error)) { std.OnConfigError(fn) }
