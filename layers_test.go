package conic

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
)

// ---- reference implementation (the pre-optimization behavior) ---------------

func refMerged(s *state) map[string]any {
	out := map[string]any{}
	mergeMaps(out, buildDefaultsTree(s.bindings))
	mergeMaps(out, s.data)
	mergeMaps(out, s.envTreeLocked())
	return out
}

func refGet(s *state, p []string) any {
	v, ok := lookup(refMerged(s), p)
	if !ok {
		return nil
	}
	return deepCopy(v)
}

func refIsSet(s *state, p []string) bool {
	if _, ok := lookup(s.data, p); ok {
		return true
	}
	_, ok := lookup(s.envTreeLocked(), p)
	return ok
}

// ---- random generators ------------------------------------------------------

// Key pool. Keys differing only in case are distinct keys and may share a map.
var keyPool = []string{"a", "A", "b", "B", "k", "K", "s", "S"}

func spell(r *rand.Rand) string { return keyPool[r.Intn(len(keyPool))] }

func genScalar(r *rand.Rand) any {
	switch r.Intn(6) {
	case 0:
		return nil
	case 1:
		return r.Intn(100)
	case 2:
		return fmt.Sprintf("s%d", r.Intn(100))
	case 3:
		return true
	case 4:
		return []any{r.Intn(10), "x", map[string]any{"in": r.Intn(5)}}
	default:
		return map[string]any{} // empty map
	}
}

func genTree(r *rand.Rand, depth int) map[string]any {
	m := map[string]any{}
	for _, i := range r.Perm(len(keyPool))[:r.Intn(5)] {
		if depth > 0 && r.Intn(10) < 6 {
			m[keyPool[i]] = genTree(r, depth-1)
		} else {
			m[keyPool[i]] = genScalar(r)
		}
	}
	return m
}

func genPath(r *rand.Rand, min, max int) []string {
	n := min + r.Intn(max-min+1)
	p := make([]string, n)
	for i := range p {
		p[i] = spell(r)
	}
	return p
}

const envPoolSize = 6

func envName(seed int64, i int) string { return fmt.Sprintf("CONIC_LAYERS_T_%d_%d", seed, i) }

// setupRandom fills s with random bindings (defaults and env tags), a random
// file layer and a random set of environment variables. With fixedEnvDepth
// the env paths never overlap, so the flat fast path must be usable.
func setupRandom(t *testing.T, s *state, seed int64, fixedEnvDepth bool) {
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < envPoolSize; i++ {
		t.Setenv(envName(seed, i), "")
		os.Unsetenv(envName(seed, i))
	}
	nb := r.Intn(3)
	for i := 0; i < nb; i++ {
		b := &binding{absPath: genPath(r, 0, 1)}
		if len(b.absPath) == 0 || r.Intn(5) > 0 {
			b.base = genTree(r, 2)
		} else {
			b.base = genScalar(r)
		}
		for j := r.Intn(4); j > 0; j-- {
			b.defaults = append(b.defaults, tagEntry{path: genPath(r, 1, 3), val: fmt.Sprintf("d%d", r.Intn(50))})
		}
		for j := r.Intn(5); j > 0; j-- {
			var p []string
			if fixedEnvDepth {
				p = genPath(r, 2, 2) // abs path has len <= 1, so total depth is fixed per binding
				p = p[:2-len(b.absPath)]
			} else {
				p = genPath(r, 1, 3)
			}
			b.envs = append(b.envs, tagEntry{path: p, val: envName(seed, r.Intn(envPoolSize))})
		}
		s.bindings = append(s.bindings, b)
	}
	s.rebuildLayersLocked()
	s.data = genTree(r, 4)
	for i := 0; i < envPoolSize; i++ {
		if r.Intn(2) == 0 {
			t.Setenv(envName(seed, i), fmt.Sprintf("e%d", i))
		}
	}
}

func allPaths(v any, prefix []string, out *[][]string) {
	*out = append(*out, prefix)
	if m, ok := v.(map[string]any); ok {
		for k, e := range m {
			allPaths(e, joinPath(prefix, k), out)
		}
	}
}

func mutate(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k := range t {
			delete(t, k)
		}
		t["zz"] = 1
	case []any:
		for i := range t {
			t[i] = "mutated"
		}
	}
}

func TestLayersMatchReference(t *testing.T) {
	var compared, nonNil, fast int
	for seed := int64(1); seed <= 200; seed++ {
		fixed := seed%2 == 0
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			c := New()
			s := c.s
			setupRandom(t, s, seed, fixed)
			if fixed && s.envs.conflict {
				t.Fatalf("fixed-depth env layer must not conflict")
			}
			if !s.defaults.conflict && !s.envs.conflict {
				fast++
			}
			r := rand.New(rand.NewSource(seed * 7919))
			var paths [][]string
			allPaths(refMerged(s), nil, &paths)
			for i := 0; i < 30; i++ {
				paths = append(paths, genPath(r, 0, 5))
			}
			for _, p := range paths {
				// random case variant of an existing path; it is a different
				// key, so it only finds what the reference finds under that
				// exact spelling
				q := make([]string, len(p))
				for i, seg := range p {
					q[i] = seg
					if r.Intn(3) == 0 {
						q[i] = strings.ToUpper(seg)
					}
				}
				for _, pp := range [][]string{p, q} {
					key := strings.Join(pp, ".")
					want := refGet(s, pp)
					got := c.Get(key)
					compared++
					if want != nil {
						nonNil++
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("Get(%q):\n got  %#v\n want %#v", key, got, want)
					}
					mutate(got)
					if again := c.Get(key); !reflect.DeepEqual(again, want) {
						t.Fatalf("Get(%q) aliases internal state after mutation:\n got  %#v\n want %#v", key, again, want)
					}
					if g, w := c.IsSet(key), refIsSet(s, pp); g != w {
						t.Fatalf("IsSet(%q) = %v, want %v", key, g, w)
					}
				}
			}
		})
	}
	t.Logf("compared %d lookups (%d non-nil), %d/200 seeds on the flat fast path", compared, nonNil, fast)
}

// Hand-written layering cases around prefixes, empty maps, nil and spelling.
func TestLayersHandwritten(t *testing.T) {
	c := New()
	s := c.s
	t.Setenv("CONIC_LH_A", "envA")
	t.Setenv("CONIC_LH_XY", "envXY")
	os.Unsetenv("CONIC_LH_UNSET")
	s.bindings = []*binding{{
		absPath: []string{"App"},
		base:    map[string]any{"Name": "n", "Sub": map[string]any{"Deep": map[string]any{}, "Leaf": 1}},
		defaults: []tagEntry{
			{path: []string{"List"}, val: "d"},
		},
		envs: []tagEntry{
			{path: []string{"a"}, val: "CONIC_LH_A"},
			{path: []string{"Sub", "X", "Y"}, val: "CONIC_LH_XY"},
			{path: []string{"Sub", "u"}, val: "CONIC_LH_UNSET"},
		},
	}}
	s.rebuildLayersLocked()
	s.data = map[string]any{
		"App": map[string]any{
			"Name": nil,
			"Sub":  map[string]any{"Deep": map[string]any{"z": 1}, "extra": []any{1, 2}},
			"a":    map[string]any{"b": 1},
		},
		"app": map[string]any{"Name": "lower"},
	}
	for _, k := range []string{"", "App", "app", "APP.Name", "app.Name", "App.Name", "App.name", "App.Sub", "App.sub", "App.Sub.Deep", "App.Sub.deep", "App.Sub.Deep.z", "App.Sub.X", "App.Sub.X.Y",
		"App.Sub.x.y", "App.Sub.X.Y.z", "App.a", "App.A", "App.a.b", "App.List", "App.list", "App.Sub.u", "nope", "App.Sub.Leaf", "App.Sub.Leaf.q"} {
		p := splitKey(k, ".")
		if got, want := c.Get(k), refGet(s, p); !reflect.DeepEqual(got, want) {
			t.Errorf("Get(%q) = %#v, want %#v", k, got, want)
		}
		if got, want := c.IsSet(k), refIsSet(s, p); got != want {
			t.Errorf("IsSet(%q) = %v, want %v", k, got, want)
		}
	}
	if got := c.Get("App.a"); got != "envA" {
		t.Errorf("env scalar must replace file map, got %#v", got)
	}
	if got := c.Get("App.Name"); got != "n" && got != nil {
		t.Errorf("App.Name = %#v", got)
	}
	if c.Get("app.Name") != "lower" || c.Get("App.A") != nil || c.Get("App.sub") != nil {
		t.Errorf("keys must match case-sensitively")
	}
	if c.s.envs.conflict || c.s.defaults.conflict {
		t.Errorf("unexpected conflict flag")
	}
}

func TestLayersEnvConflictFallback(t *testing.T) {
	c := New()
	s := c.s
	t.Setenv("CONIC_LC_1", "one")
	t.Setenv("CONIC_LC_2", "two")
	s.bindings = []*binding{{
		absPath: []string{"a"},
		envs: []tagEntry{
			{path: []string{"b"}, val: "CONIC_LC_1"},
			{path: []string{"b", "c"}, val: "CONIC_LC_2"},
		},
	}}
	s.rebuildLayersLocked()
	if !s.envs.conflict {
		t.Fatal("overlapping env paths should flag the layer")
	}
	s.data = map[string]any{"a": map[string]any{"b": map[string]any{"q": 1}}}
	for _, k := range []string{"", "a", "a.b", "a.b.c", "a.b.q"} {
		p := splitKey(k, ".")
		if got, want := c.Get(k), refGet(s, p); !reflect.DeepEqual(got, want) {
			t.Errorf("Get(%q) = %#v, want %#v", k, got, want)
		}
		if got, want := c.IsSet(k), refIsSet(s, p); got != want {
			t.Errorf("IsSet(%q) = %v, want %v", k, got, want)
		}
	}
}

// ---- NUL validation -----------------------------------------------------------

func TestNulKeyInFile(t *testing.T) {
	c := New()
	var cfg server
	if err := c.BindRef("srv", &cfg); err != nil {
		t.Fatal(err)
	}
	p := writeTemp(t, "c.json", `{"srv":{"host":"h","port":1}}`)
	c.SetConfigFile(p)
	if err := c.ReadConfig(); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"top":    `{"a\u0000b":1}`,
		"nested": `{"srv":{"host":"x","deep":{"k\u0000":1}}}`,
		"slice":  `{"srv":{"host":"x"},"l":[{"a\u0000":1}]}`,
	} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		err := c.ReadConfig()
		var pe ConfigParseError
		if !errors.As(err, &pe) {
			t.Fatalf("%s: err = %v, want ConfigParseError", name, err)
		}
		if cfg.Host != "h" || cfg.Port != 1 || c.GetString("srv.host") != "h" {
			t.Fatalf("%s: old data changed: %+v", name, cfg)
		}
	}
}

func TestNulKeyInSetAndGet(t *testing.T) {
	c := New()
	var cfg server
	if err := c.BindRef("srv", &cfg); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("srv.host", "h"); err != nil {
		t.Fatal(err)
	}
	before := c.Get("")
	for _, tc := range []struct {
		key string
		val any
	}{
		{"a\x00b", 1},
		{"srv.host", map[string]any{"x\x00": 1}},
		{"m", map[string]any{"ok": map[string]any{"\x00": 1}}},
		{"m", []any{map[string]any{"b\x00": 1}}},
		{"", map[string]any{"q\x00": 1}},
	} {
		err := c.Set(tc.key, tc.val)
		var be BindError
		if !errors.As(err, &be) {
			t.Errorf("Set(%q, %v) err = %v, want BindError", tc.key, tc.val, err)
		}
	}
	if after := c.Get(""); !reflect.DeepEqual(before, after) || cfg.Host != "h" {
		t.Errorf("failed Set changed state: %#v -> %#v", before, after)
	}
	if v := c.Get("a\x00b"); v != nil {
		t.Errorf("Get with NUL = %#v, want nil", v)
	}
	if c.IsSet("a\x00b") || c.Sub("srv\x00").IsSet("host") || c.Sub("srv\x00").Get("host") != nil {
		t.Error("NUL keys must not be found")
	}
}
