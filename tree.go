package conic

import (
	"fmt"
	"strings"
)

func splitKey(key, delim string) []string {
	if key == "" {
		return nil
	}
	return strings.Split(key, delim)
}

// joinPath returns a fresh slice holding a followed by b.
func joinPath(a []string, b ...string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

// lookup finds the value at path inside tree.
func lookup(tree any, path []string) (any, bool) {
	cur := tree
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, found := m[seg]
		if !found {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// parentOf walks to the map holding the last segment of path (len(path) > 0),
// creating intermediate maps (replacing non-map values) as needed. It returns
// that map and the last segment.
func parentOf(root map[string]any, path []string) (map[string]any, string) {
	m := root
	for _, k := range path[:len(path)-1] {
		next, isMap := m[k].(map[string]any)
		if !isMap || next == nil {
			next = map[string]any{}
			m[k] = next
		}
		m = next
	}
	return m, path[len(path)-1]
}

// setPath replaces the value at path (len(path) > 0), creating maps on the way.
func setPath(root map[string]any, path []string, val any) {
	m, key := parentOf(root, path)
	m[key] = val
}

// mergeAt deep-merges val into root at path. With an empty path val must be a
// map and is merged into root itself.
func mergeAt(root map[string]any, path []string, val any) {
	if len(path) == 0 {
		if vm, ok := val.(map[string]any); ok {
			mergeMaps(root, vm)
		}
		return
	}
	m, key := parentOf(root, path)
	dm, dok := m[key].(map[string]any)
	vm, vok := val.(map[string]any)
	if dok && vok && dm != nil {
		mergeMaps(dm, vm)
		return
	}
	m[key] = deepCopy(val)
}

// mergeMaps deep-merges src into dst. Maps merge recursively; everything else
// (slices included) is replaced. Keys match exactly (case-sensitively).
func mergeMaps(dst, src map[string]any) {
	for k, v := range src {
		if sm, sok := v.(map[string]any); sok {
			if dm, dok := dst[k].(map[string]any); dok && dm != nil {
				mergeMaps(dm, sm)
				continue
			}
		}
		dst[k] = deepCopy(v)
	}
}

// deleteAt removes the value at path, if any.
func deleteAt(root map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	parent, ok := lookup(root, path[:len(path)-1])
	if !ok {
		return
	}
	pm, ok := parent.(map[string]any)
	if !ok {
		return
	}
	delete(pm, path[len(path)-1])
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if t == nil {
			return t
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		if t == nil {
			return t
		}
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	default:
		return v
	}
}

// normalize converts map[any]any (as produced by some YAML documents) into
// map[string]any recursively, including inside slices.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalize(e)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[fmt.Sprint(k)] = normalize(e)
		}
		return out
	case []any:
		for i, e := range t {
			t[i] = normalize(e)
		}
		return t
	default:
		return v
	}
}
