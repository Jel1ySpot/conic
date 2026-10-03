package conic

import (
	"errors"
	"os"
	"sort"
	"strings"
)

// This file implements the cached "flat" default and environment layers and
// the layer-by-layer lookup used by Get and IsSet. The result of a lookup is
// identical to deepCopy(lookup(mergedLocked(), p)) but never builds the merged
// tree.

const keySep = "\x00"

// keyPath writes the NUL-joined key of p into buf and records in ends where
// each prefix of the key ends: buf[:ends[i]] is the key of p[:i+1].
func keyPath(buf []byte, ends []int, p []string) ([]byte, []int) {
	for i, seg := range p {
		if i > 0 {
			buf = append(buf, 0)
		}
		buf = append(buf, seg...)
		ends = append(ends, len(buf))
	}
	return buf, ends
}

func hasNulPath(p []string) bool {
	for _, s := range p {
		if strings.IndexByte(s, 0) >= 0 {
			return true
		}
	}
	return false
}

// hasNulKey reports whether any map key anywhere inside v contains a NUL.
func hasNulKey(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if strings.IndexByte(k, 0) >= 0 || hasNulKey(e) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if hasNulKey(e) {
				return true
			}
		}
	}
	return false
}

var errNulKey = errors.New("keys must not contain NUL (\\x00)")

type span struct{ lo, hi int32 }

type flatEntry struct {
	key  string   // NUL-joined path
	path []string // path segments
	val  any      // default value, or the environment variable name
	seq  int      // insertion order
}

// flatLayer is an immutable, flattened view of a layer made of default values
// or environment variable names. Entries are sorted by key, so the strict
// descendants of any path form a contiguous range.
type flatLayer struct {
	isEnv   bool
	entries []flatEntry
	exact   map[string]span     // key -> entries with exactly that key
	below   map[string]span     // non-root strict prefix -> its strict descendants
	first   map[string]struct{} // first segments
	// conflict means the layer cannot be answered from the flat form (NUL in a
	// key, overlapping paths, ...); callers fall back to the merged tree.
	conflict bool
}

func buildFlat(entries []flatEntry, isEnv bool) *flatLayer {
	l := &flatLayer{isEnv: isEnv}
	if len(entries) == 0 {
		return l
	}
	var buf []byte
	for i := range entries {
		e := &entries[i]
		if hasNulPath(e.path) {
			l.conflict = true
		}
		buf, _ = keyPath(buf[:0], nil, e.path)
		e.key = string(buf)
		e.seq = i
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	l.entries = entries
	l.exact = map[string]span{}
	l.below = map[string]span{}
	l.first = map[string]struct{}{}
	extend := func(m map[string]span, k string, i int32) {
		if sp, ok := m[k]; ok {
			sp.hi = i + 1
			m[k] = sp
		} else {
			m[k] = span{i, i + 1}
		}
	}
	for i, e := range entries {
		idx := int32(i)
		extend(l.exact, e.key, idx)
		fe := len(e.key)
		for j := 0; j < len(e.key); j++ {
			if e.key[j] == 0 {
				extend(l.below, e.key[:j], idx)
				if fe == len(e.key) {
					fe = j
				}
			}
		}
		l.first[e.key[:fe]] = struct{}{}
	}
	for k := range l.exact {
		if _, ok := l.below[k]; ok {
			l.conflict = true // a path is both a value and a parent
		}
	}
	return l
}

// flattenTree turns a tree into entries: every non-map value and every empty
// map is a leaf.
func flattenTree(t map[string]any, path []string, out []flatEntry) []flatEntry {
	for k, v := range t {
		p := joinPath(path, k)
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			out = flattenTree(m, p, out)
			continue
		}
		out = append(out, flatEntry{path: p, val: v})
	}
	return out
}

func buildDefaultsLayer(tree map[string]any) *flatLayer {
	return buildFlat(flattenTree(tree, nil, nil), false)
}

func buildEnvLayer(bindings []*binding) *flatLayer {
	var entries []flatEntry
	for _, b := range bindings {
		for _, e := range b.envs {
			if p := joinPath(b.absPath, e.path...); len(p) > 0 {
				entries = append(entries, flatEntry{path: p, val: e.val})
			}
		}
	}
	return buildFlat(entries, true)
}

// buildDefaultsTree builds the defaults tree of the given bindings.
func buildDefaultsTree(bindings []*binding) map[string]any {
	t := map[string]any{}
	for _, b := range bindings {
		mergeAt(t, b.absPath, b.base)
		for _, d := range b.defaults {
			if p := joinPath(b.absPath, d.path...); len(p) > 0 {
				setPath(t, p, d.val)
			}
		}
	}
	return t
}

// rebuildLayersLocked recomputes the cached default tree and flat layers from
// the current bindings.
func (s *state) rebuildLayersLocked() {
	s.defaultsTree = buildDefaultsTree(s.bindings)
	s.defaults = buildDefaultsLayer(s.defaultsTree)
	s.envs = buildEnvLayer(s.bindings)
}

// isScalar reports whether the entries in sp make the path a non-map value.
func (l *flatLayer) isScalar(sp span) bool {
	if !l.isEnv {
		_, isMap := l.entries[sp.hi-1].val.(map[string]any)
		return !isMap
	}
	for i := sp.lo; i < sp.hi; i++ {
		if _, ok := os.LookupEnv(l.entries[i].val.(string)); ok {
			return true
		}
	}
	return false
}

// envValue returns the effective value of the entries in sp: the last one
// whose variable is set.
func (l *flatLayer) envValue(sp span) (string, bool) {
	for i := sp.hi - 1; i >= sp.lo; i-- {
		if v, ok := os.LookupEnv(l.entries[i].val.(string)); ok {
			return v, true
		}
	}
	return "", false
}

type setEntry struct {
	seq  int
	path []string
	val  any
}

// assemble builds the map of the entries in sp, relative to a path of n
// segments. The result is fresh; ok is false if it would be empty.
func (l *flatLayer) assemble(sp span, n int) (map[string]any, bool) {
	var es []setEntry
	for i := sp.lo; i < sp.hi; i++ {
		e := &l.entries[i]
		if l.isEnv {
			v, ok := os.LookupEnv(e.val.(string))
			if !ok {
				continue
			}
			es = append(es, setEntry{e.seq, e.path[n:], v})
		} else {
			es = append(es, setEntry{e.seq, e.path[n:], deepCopy(e.val)})
		}
	}
	if len(es) == 0 {
		return nil, false
	}
	if l.isEnv {
		sort.Slice(es, func(i, j int) bool { return es[i].seq < es[j].seq })
	}
	out := map[string]any{}
	for _, e := range es {
		setPath(out, e.path, e.val)
	}
	return out, true
}

// query looks up the value of the layer at the path whose key and
// prefix ends are buf and ends. scalar reports that a strict prefix holds a
// non-map value (only checked when checkScalar is set). fresh reports that v
// is not shared with the cache.
func (l *flatLayer) query(n int, buf []byte, ends []int, checkScalar bool) (v any, found, scalar, fresh bool) {
	if len(l.entries) == 0 {
		return
	}
	if n == 0 {
		m, ok := l.assemble(span{0, int32(len(l.entries))}, 0)
		return m, ok, false, true
	}
	if _, ok := l.first[string(buf[:ends[0]])]; !ok {
		return
	}
	if checkScalar {
		for i := 0; i < n-1; i++ {
			if sp, ok := l.exact[string(buf[:ends[i]])]; ok && l.isScalar(sp) {
				return nil, false, true, false
			}
		}
	}
	key := buf[:ends[n-1]]
	if sp, ok := l.exact[string(key)]; ok {
		if !l.isEnv {
			return l.entries[sp.hi-1].val, true, false, false
		}
		s, ok := l.envValue(sp)
		return s, ok, false, true
	}
	if sp, ok := l.below[string(key)]; ok {
		m, ok := l.assemble(sp, n)
		return m, ok, false, true
	}
	return
}

// anySet reports whether the layer has a set entry at or below the path.
func (l *flatLayer) anySet(n int, buf []byte, ends []int) bool {
	if len(l.entries) == 0 {
		return false
	}
	if _, ok := l.first[string(buf[:ends[0]])]; !ok {
		return false
	}
	key := buf[:ends[n-1]]
	if sp, ok := l.exact[string(key)]; ok && l.isScalar(sp) {
		return true
	}
	if sp, ok := l.below[string(key)]; ok {
		for i := sp.lo; i < sp.hi; i++ {
			if _, ok := os.LookupEnv(l.entries[i].val.(string)); ok {
				return true
			}
		}
	}
	return false
}

// mergeValue merges v into res with mergeMaps semantics and returns the new
// value. res must be owned by the caller; v is not modified or retained.
func mergeValue(res, v any) any {
	if sm, ok := v.(map[string]any); ok {
		if dm, ok := res.(map[string]any); ok && dm != nil {
			mergeMaps(dm, sm)
			return dm
		}
	}
	return deepCopy(v)
}

// dataLookup walks the file layer. scalar reports that a strict prefix of p
// holds a non-map value (nil included).
func dataLookup(data map[string]any, p []string) (v any, found, scalar bool) {
	var cur any = data
	for _, seg := range p {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false, true
		}
		v, ok := m[seg]
		if !ok {
			return nil, false, false
		}
		cur = v
	}
	return cur, true, false
}

// getLocked returns a copy of the merged value at p, equal to
// deepCopy(lookup(mergedLocked(), p)).
func (s *state) getLocked(p []string) (any, bool) {
	if hasNulPath(p) {
		return nil, false
	}
	if s.defaults.conflict || s.envs.conflict {
		v, ok := lookup(s.mergedLocked(), p)
		return v, ok
	}
	var bufA [128]byte
	var endsA [16]int
	var buf []byte
	var ends []int
	if len(s.defaults.entries) > 0 || len(s.envs.entries) > 0 {
		buf, ends = keyPath(bufA[:0], endsA[:0], p)
	}
	var res any
	exists := false
	if len(p) == 0 {
		res, exists = map[string]any{}, true
	}
	apply := func(v any, found, scalar, fresh bool) {
		if scalar {
			res, exists = nil, false
		}
		if !found {
			return
		}
		switch {
		case exists:
			res = mergeValue(res, v)
		case fresh:
			res, exists = v, true
		default:
			res, exists = deepCopy(v), true
		}
	}
	apply(s.defaults.query(len(p), buf, ends, exists))
	{
		v, found, scalar := dataLookup(s.data, p)
		apply(v, found, scalar, false)
	}
	apply(s.envs.query(len(p), buf, ends, exists))
	return res, exists
}

// isSetLocked reports whether p is in the file layer or set (or has a set
// descendant) in the environment layer.
func (s *state) isSetLocked(p []string) bool {
	if hasNulPath(p) {
		return false
	}
	if _, ok := lookup(s.data, p); ok {
		return true
	}
	if s.envs.conflict {
		_, ok := lookup(s.envTreeLocked(), p)
		return ok
	}
	if len(p) == 0 || len(s.envs.entries) == 0 {
		return false
	}
	var bufA [128]byte
	var endsA [16]int
	buf, ends := keyPath(bufA[:0], endsA[:0], p)
	return s.envs.anySet(len(p), buf, ends)
}
