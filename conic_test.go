package conic

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readTemp(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type server struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

func TestExtensions(t *testing.T) {
	cases := map[string]string{
		"c.yml":  "host: a\nport: 1\n",
		"c.yaml": "host: a\nport: 1\n",
		"c.json": `{"host":"a","port":1}`,
		"c.YML":  "host: a\nport: 1\n",
		"c.JSON": `{"host":"a","port":1}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeTemp(t, name, content)
			c := New()
			c.SetConfigFile(p)
			var s server
			if err := c.BindRef("", &s); err != nil {
				t.Fatal(err)
			}
			if err := c.ReadConfig(); err != nil {
				t.Fatal(err)
			}
			if s.Host != "a" || s.Port != 1 {
				t.Fatalf("got %+v", s)
			}
			s.Port = 2
			if err := c.WriteConfig(); err != nil {
				t.Fatal(err)
			}
			var s2 server
			c2 := New()
			c2.SetConfigFile(p)
			_ = c2.BindRef("", &s2)
			if err := c2.ReadConfig(); err != nil || s2.Port != 2 {
				t.Fatalf("err=%v s2=%+v", err, s2)
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		p := writeTemp(t, "c.toml", "x=1")
		c := New()
		c.SetConfigFile(p)
		for _, err := range []error{c.ReadConfig(), c.WriteConfig()} {
			var ue UnsupportedConfigError
			if !errors.As(err, &ue) {
				t.Fatalf("want UnsupportedConfigError, got %v", err)
			}
		}
		if err := c.SetConfigType("toml"); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestBindDeepMissingPathAndNewFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "dir", "c.yaml")
	c := New()
	c.SetConfigFile(p)
	db := server{Host: "h", Port: 5}
	if err := c.BindRef("server.db", &db); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "server:") || !strings.Contains(out, "host: h") {
		t.Fatalf("unexpected file:\n%s", out)
	}
	if c.GetString("server.db.host") != "h" {
		t.Fatal("Get failed")
	}
}

func TestSub(t *testing.T) {
	p := writeTemp(t, "c.yaml", "server:\n  host: x\n  port: 9\n  db:\n    host: dbh\n")
	c := New()
	c.SetConfigFile(p)
	var s server
	sub := c.Sub("server")
	if err := sub.BindRef("", &s); err != nil {
		t.Fatal(err)
	}
	if len(c.s.bindings) != 1 {
		t.Fatalf("bindings=%d", len(c.s.bindings))
	}
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if s.Host != "x" || s.Port != 9 {
		t.Fatalf("%+v", s)
	}
	var db server
	subsub := sub.Sub("db")
	if err := subsub.BindRef("", &db); err != nil {
		t.Fatal(err)
	}
	if db.Host != "dbh" {
		t.Fatalf("%+v", db)
	}
	if sub.GetInt("port") != 9 || subsub.GetString("host") != "dbh" || c.GetString("server.db.host") != "dbh" {
		t.Fatal("relative Get failed")
	}
	// prefix aliasing
	a := c.Sub("a")
	b1, b2 := a.Sub("b"), a.Sub("c")
	if b1.path("k")[1] != "b" || b2.path("k")[1] != "c" {
		t.Fatal("prefix aliasing")
	}
	s.Port = 10
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "port: 10") || !strings.Contains(out, "dbh") {
		t.Fatalf("file:\n%s", out)
	}
	if err := sub.Set("port", 11); err != nil || s.Port != 11 {
		t.Fatalf("err=%v s=%+v", err, s)
	}
}

func TestErrors(t *testing.T) {
	c := New()
	if err := c.ReadConfig(); !errors.As(err, new(NoConfigFileError)) {
		t.Fatalf("got %v", err)
	}
	if err := c.WriteConfig(); !errors.As(err, new(NoConfigFileError)) {
		t.Fatalf("got %v", err)
	}
	c.SetConfigFile(filepath.Join(t.TempDir(), "missing.yaml"))
	err := c.ReadConfig()
	var nf ConfigFileNotFoundError
	if !errors.Is(err, fs.ErrNotExist) || !errors.As(err, &nf) {
		t.Fatalf("got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "conic: ") {
		t.Fatal(err.Error())
	}

	p := writeTemp(t, "c.json", "{bad")
	c2 := New()
	c2.SetConfigFile(p)
	var s server
	_ = c2.BindRef("", &s)
	s.Host = "keep"
	err = c2.ReadConfig()
	var pe ConfigParseError
	if !errors.As(err, &pe) {
		t.Fatalf("got %v", err)
	}
	if s.Host != "keep" {
		t.Fatal("struct changed on parse error")
	}
	var bs struct{}
	if err := c2.BindRef("x", bs); !errors.As(err, new(BindError)) {
		t.Fatalf("got %v", err)
	}
	if err := c2.BindRef("x", (*server)(nil)); !errors.As(err, new(BindError)) {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownKeysPreserved(t *testing.T) {
	p := writeTemp(t, "c.yaml", "host: a\nport: 1\nextra:\n  k: v\n")
	c := New()
	c.SetConfigFile(p)
	var s server
	_ = c.BindRef("", &s)
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("extra.k") != "v" {
		t.Fatal("extra not readable")
	}
	s.Port = 3
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "extra:") || !strings.Contains(out, "k: v") {
		t.Fatalf("lost extra:\n%s", out)
	}
}

func TestGetSetIsSet(t *testing.T) {
	p := writeTemp(t, "c.json", `{"a":{"b":{"n":12.7,"s":"42","t":true,"name":"x"}}}`)
	c := New()
	c.SetConfigFile(p)
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if c.GetInt("a.b.n") != 12 || c.GetInt("a.b.s") != 42 || c.GetInt("a.b.t") != 1 || c.GetInt("a.b.name") != 0 {
		t.Fatal("GetInt")
	}
	if c.GetString("a.b.n") != "12.7" || c.GetString("a.b.t") != "true" || c.GetString("nope") != "" {
		t.Fatal("GetString")
	}
	if c.Get("nope.x") != nil || c.Get("a.b.name.x") != nil {
		t.Fatal("missing should be nil")
	}
	if !c.IsSet("a.b") || c.IsSet("a.zzz") {
		t.Fatal("IsSet")
	}
	if c.GetString("A.B.NAME") != "" || c.GetString("a.b.name") != "x" {
		t.Fatal("case-sensitive")
	}
	m := c.Get("a").(map[string]any)
	m["b"] = 1
	if _, ok := c.Get("a.b").(map[string]any); !ok {
		t.Fatal("Get must return a copy")
	}
	if err := c.Set("x.y.z", 5); err != nil || c.GetInt("x.y.z") != 5 || !c.IsSet("x.y.z") {
		t.Fatalf("Set err=%v", err)
	}
}

type setCfg struct {
	Name string `mapstructure:"name"`
	Port int    `mapstructure:"port"`
}

func TestSetBehaviors(t *testing.T) {
	c := New()
	var s setCfg
	_ = c.BindRef("app", &s)
	s.Name = "unsaved"
	if err := c.Set("app.port", 80); err != nil {
		t.Fatal(err)
	}
	if s.Port != 80 || s.Name != "unsaved" {
		t.Fatalf("%+v", s)
	}
	err := c.Set("app.port", "abc")
	if !errors.As(err, new(BindError)) {
		t.Fatalf("got %v", err)
	}
	if s.Port != 80 || c.GetInt("app.port") != 80 {
		t.Fatalf("not rolled back: %+v %v", s, c.Get("app.port"))
	}
	if err := c.Set("app", map[string]any{"name": "n2"}); err != nil || s.Name != "n2" {
		t.Fatalf("err=%v %+v", err, s)
	}
}

type tagCfg struct {
	Port    int           `mapstructure:"port" default:"8080" env:"CONIC_T_PORT"`
	Timeout time.Duration `mapstructure:"timeout" default:"1s"`
	Tags    []string      `mapstructure:"tags" default:"a,b"`
	Debug   bool          `mapstructure:"debug" default:"true"`
	Inner   struct {
		Level string `mapstructure:"level" default:"info"`
	} `mapstructure:"inner"`
}

func TestTags(t *testing.T) {
	c := New()
	var cfg tagCfg
	if err := c.BindRef("", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 || cfg.Timeout != time.Second || len(cfg.Tags) != 2 || cfg.Tags[1] != "b" || !cfg.Debug || cfg.Inner.Level != "info" {
		t.Fatalf("defaults: %+v", cfg)
	}
	if c.IsSet("port") {
		t.Fatal("defaults must not count as set")
	}

	p := writeTemp(t, "c.yaml", "port: 9000\ntimeout: 3m\ntags: [x, y, z]\ninner:\n  level: warn\n")
	c.SetConfigFile(p)
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9000 || cfg.Timeout != 3*time.Minute || len(cfg.Tags) != 3 || cfg.Inner.Level != "warn" {
		t.Fatalf("file: %+v", cfg)
	}

	t.Setenv("CONIC_T_PORT", "7777")
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7777 || c.GetInt("port") != 7777 || !c.IsSet("port") {
		t.Fatalf("env: %+v", cfg)
	}
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	out := readTemp(t, p)
	if !strings.Contains(out, "port: 9000") || strings.Contains(out, "7777") {
		t.Fatalf("env leaked:\n%s", out)
	}
}

type shared struct {
	Name    string            `mapstructure:"name"`
	Skip    string            `mapstructure:"-"`
	Opt     string            `mapstructure:"opt,omitempty"`
	Timeout time.Duration     `mapstructure:"timeout"`
	Base    base              `mapstructure:",squash"`
	Extra   map[string]any    `mapstructure:",remain"`
	Labels  map[string]string `mapstructure:"labels"`
}

type base struct {
	ID int `mapstructure:"id"`
}

func TestMapstructureTags(t *testing.T) {
	dir := t.TempDir()
	jp := filepath.Join(dir, "c.json")
	yp := filepath.Join(dir, "c.yaml")
	_ = os.WriteFile(jp, []byte(`{"name":"n","id":7,"timeout":"2s","labels":{"a":"b"},"foo":"bar"}`), 0o644)
	_ = os.WriteFile(yp, []byte("name: n\nid: 7\ntimeout: 2s\nlabels:\n  a: b\nfoo: bar\n"), 0o644)
	var res [2]shared
	for i, p := range []string{jp, yp} {
		c := New()
		c.SetConfigFile(p)
		res[i].Skip = "init"
		if err := c.BindRef("", &res[i]); err != nil {
			t.Fatal(err)
		}
		if err := c.ReadConfig(); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range res {
		if r.Name != "n" || r.Base.ID != 7 || r.Timeout != 2*time.Second || r.Labels["a"] != "b" || r.Skip != "init" || r.Extra["foo"] != "bar" {
			t.Fatalf("%+v", r)
		}
	}

	// writing: squash, omitempty, "-"
	c := New()
	p := filepath.Join(dir, "out.yaml")
	c.SetConfigFile(p)
	v := shared{Name: "w", Skip: "secret", Base: base{ID: 3}, Timeout: time.Minute}
	_ = c.BindRef("", &v)
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	out := readTemp(t, p)
	if strings.Contains(out, "secret") || strings.Contains(out, "opt") || !strings.Contains(out, "id: 3") || !strings.Contains(out, "timeout: 1m0s") {
		t.Fatalf("out:\n%s", out)
	}
}

func TestOmitemptyDeletesKey(t *testing.T) {
	type o struct {
		A string `mapstructure:"a,omitempty"`
		B string `mapstructure:"b"`
	}
	p := writeTemp(t, "c.yaml", "a: x\nb: y\n")
	c := New()
	c.SetConfigFile(p)
	var v o
	_ = c.BindRef("", &v)
	_ = c.ReadConfig()
	v.A = ""
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	if out := readTemp(t, p); strings.Contains(out, "a:") {
		t.Fatalf("a not removed:\n%s", out)
	}
}

func TestNonStructRefs(t *testing.T) {
	p := writeTemp(t, "c.yaml", "m:\n  k: v\nl: [a, b]\nn: 5\n")
	c := New()
	c.SetConfigFile(p)
	var m map[string]any
	var l []string
	n := 1
	_ = c.BindRef("m", &m)
	_ = c.BindRef("l", &l)
	_ = c.BindRef("n", &n)
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if m["k"] != "v" || len(l) != 2 || n != 5 {
		t.Fatalf("%v %v %v", m, l, n)
	}
	n = 6
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTemp(t, p), "6") {
		t.Fatalf("n not written: %s", readTemp(t, p))
	}
}

func TestRemovedKeyFallsBack(t *testing.T) {
	type cfg struct {
		Port int    `mapstructure:"port" default:"80"`
		Name string `mapstructure:"name"`
	}
	p := writeTemp(t, "c.yaml", "port: 1\nname: file\n")
	c := New()
	c.SetConfigFile(p)
	v := cfg{Name: "init"}
	_ = c.BindRef("", &v)
	_ = c.ReadConfig()
	if v.Port != 1 || v.Name != "file" {
		t.Fatalf("%+v", v)
	}
	_ = os.WriteFile(p, []byte("other: 1\n"), 0o644)
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if v.Port != 80 || v.Name != "init" {
		t.Fatalf("%+v", v)
	}
}

func TestReadTransactional(t *testing.T) {
	p := writeTemp(t, "c.yaml", "a: 1\nb: notanint\n")
	c := New()
	c.SetConfigFile(p)
	var a, b struct {
		V int `mapstructure:"v"`
	}
	_ = c.BindRef("a", &a)
	_ = c.BindRef("b", &b)
	_ = os.WriteFile(p, []byte("a:\n  v: 5\nb:\n  v: oops\n"), 0o644)
	err := c.ReadConfig()
	if !errors.As(err, new(BindError)) {
		t.Fatalf("got %v", err)
	}
	if a.V != 0 || c.IsSet("a.v") {
		t.Fatal("partial commit")
	}
}

func TestEmptyFile(t *testing.T) {
	for _, n := range []string{"c.yaml", "c.json"} {
		p := writeTemp(t, n, "")
		c := New()
		c.SetConfigFile(p)
		if err := c.ReadConfig(); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
}

func TestYamlNonStringKeys(t *testing.T) {
	p := writeTemp(t, "c.yaml", "m:\n  1: one\n  2: two\nl:\n  - 3: x\n")
	c := New()
	c.SetConfigFile(p)
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if c.GetString("m.1") != "one" {
		t.Fatal("m.1")
	}
	if err := c.WriteConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestLogger(t *testing.T) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	p := writeTemp(t, "c.yaml", "a: 1\n")
	c := New()
	c.SetConfigFile(p)
	_ = c.ReadConfig()
	_ = c.WriteConfig()
	os.Stdout = old
	w.Close()
	b, _ := io.ReadAll(r)
	if len(b) != 0 {
		t.Fatalf("unexpected output %q", b)
	}

	var msgs []string
	c.SetLogger(func(f string, a ...any) { msgs = append(msgs, f) })
	_ = c.ReadConfig()
	if len(msgs) == 0 {
		t.Fatal("no log received")
	}
}

func TestHooks(t *testing.T) {
	p := writeTemp(t, "c.yaml", "a: 1\n")
	c := New()
	c.SetConfigFile(p)
	var calls []int
	c.OnConfigLoad(func() { calls = append(calls, c.GetInt("a")) })
	c.OnConfigLoad(func() { calls = append(calls, -1) })
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != 1 || calls[1] != -1 {
		t.Fatalf("%v", calls)
	}
	called := false
	c.OnConfigError(func(error) { called = true })
	_ = os.WriteFile(p, []byte(":::bad: ["), 0o644)
	if err := c.ReadConfig(); err == nil || called || len(calls) != 2 {
		t.Fatal("failed manual read must return error and not fire hooks")
	}
	c.s.fireError(errors.New("x"))
	if !called {
		t.Fatal("fireError")
	}
	if !c.s.sameAsLast([]byte("a: 1\n")) {
		t.Fatal("sameAsLast")
	}
}

func TestConcurrency(t *testing.T) {
	p := writeTemp(t, "c.yaml", "port: 1\nhost: h\n")
	c := New()
	c.SetConfigFile(p)
	var s server
	_ = c.BindRef("", &s)
	_ = c.ReadConfig()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				switch i % 4 {
				case 0:
					_ = c.GetInt("port")
					_ = c.Get("")
				case 1:
					_ = c.Set("port", j)
				case 2:
					c.View(func() { _ = s.Port })
					c.Update(func() { s.Host = "x" })
				case 3:
					_ = c.ReadConfig()
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestPackageLevel(t *testing.T) {
	if GetConic() == nil {
		t.Fatal("nil")
	}
	old := std
	std = New()
	defer func() { std = old }()
	p := writeTemp(t, "c.yaml", "a: 1\n")
	SetConfigFile(p)
	if err := ReadConfig(); err != nil || GetInt("a") != 1 {
		t.Fatal("pkg level")
	}
}

func TestIncrementalSyncKeepsDefaults(t *testing.T) {
	type cfgT struct {
		Name string `mapstructure:"name"`
		Port int    `mapstructure:"port" default:"8080"`
	}
	setup := func(t *testing.T) (*Conic, *cfgT, string) {
		p := writeTemp(t, "c.json", `{"name":"a"}`)
		c := New()
		c.SetConfigFile(p)
		cfg := &cfgT{}
		if err := c.BindRef("", cfg); err != nil {
			t.Fatal(err)
		}
		if err := c.ReadConfig(); err != nil {
			t.Fatal(err)
		}
		return c, cfg, p
	}
	c, cfg, _ := setup(t)
	if err := c.Set("name", "b"); err != nil {
		t.Fatal(err)
	}
	if c.IsSet("port") || c.GetInt("port") != 8080 || cfg.Name != "b" {
		t.Fatal("Set wrote default")
	}
	c, cfg, _ = setup(t)
	c.Update(func() { cfg.Name = "c" })
	if c.IsSet("port") || c.GetInt("port") != 8080 || c.GetString("name") != "c" {
		t.Fatal("Update wrote default")
	}
	if err := c.WriteConfig(); err != nil || !c.IsSet("port") {
		t.Fatal("WriteConfig should persist defaults")
	}

	c, cfg, _ = setup(t)
	c.Update(func() { cfg.Port = 9000 })
	if !c.IsSet("port") || c.GetInt("port") != 9000 {
		t.Fatal("changed value must be set")
	}

	c, cfg, _ = setup(t)
	cfg.Port = 1234
	if err := c.Set("name", "z"); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 1234 {
		t.Fatal("unsaved change lost")
	}
}

// ---- case-sensitive keys ------------------------------------------------------

type untagged struct {
	Name string `default:"def"`
}

func TestCaseSensitiveUntaggedField(t *testing.T) {
	c := New()
	var cfg untagged
	if err := c.BindRef("", &cfg); err != nil {
		t.Fatal(err)
	}
	c.SetConfigFile(writeTemp(t, "c.json", `{"name":"file"}`))
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "def" {
		t.Fatalf("Name = %q, want def", cfg.Name)
	}
	if c.Get("name") != "file" || c.Get("Name") != "def" {
		t.Fatalf("name=%v Name=%v", c.Get("name"), c.Get("Name"))
	}
	all, ok := c.Get("").(map[string]any)
	if !ok || all["Name"] != "def" || all["name"] != "file" {
		t.Fatalf("Get(\"\") = %#v", c.Get(""))
	}
}

func TestCaseSensitiveTaggedField(t *testing.T) {
	c := New()
	var cfg struct {
		Name string `mapstructure:"name"`
	}
	if err := c.BindRef("", &cfg); err != nil {
		t.Fatal(err)
	}
	c.SetConfigFile(writeTemp(t, "c.json", `{"Name":"x"}`))
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "" {
		t.Fatalf("Name = %q, want empty", cfg.Name)
	}
}

func TestCaseSensitiveGetIsSet(t *testing.T) {
	c := New()
	c.SetConfigFile(writeTemp(t, "c.json", `{"server":{"port":1}}`))
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if c.Get("Server.Port") != nil || c.Get("server.Port") != nil || c.Get("SERVER") != nil {
		t.Fatal("variants must not be found")
	}
	if c.IsSet("Server.Port") || c.IsSet("server.Port") || c.IsSet("SERVER") {
		t.Fatal("variants must not be set")
	}
	if !c.IsSet("server.port") || c.GetInt("server.port") != 1 {
		t.Fatal("exact key must be found")
	}
}

func TestCaseSensitiveSet(t *testing.T) {
	c := New()
	c.SetConfigFile(writeTemp(t, "c.json", `{"name":"file"}`))
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("Name", "other"); err != nil {
		t.Fatal(err)
	}
	if c.Get("name") != "file" || c.Get("Name") != "other" {
		t.Fatalf("name=%v Name=%v", c.Get("name"), c.Get("Name"))
	}
}
