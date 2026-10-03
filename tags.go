package conic

import (
	"encoding"
	"reflect"
	"strings"
	"time"
)

var (
	durationType        = reflect.TypeOf(time.Duration(0))
	timeType            = reflect.TypeOf(time.Time{})
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// fieldInfo is the parsed mapstructure view of a struct field.
type fieldInfo struct {
	name      string
	skip      bool
	squash    bool
	omitempty bool
	remain    bool
}

func parseField(sf reflect.StructField) fieldInfo {
	if sf.PkgPath != "" { // unexported
		return fieldInfo{skip: true}
	}
	tag := sf.Tag.Get("mapstructure")
	if tag == "-" {
		return fieldInfo{skip: true}
	}
	parts := strings.Split(tag, ",")
	fi := fieldInfo{name: parts[0]}
	if fi.name == "" {
		fi.name = sf.Name
	}
	for _, opt := range parts[1:] {
		switch opt {
		case "squash":
			fi.squash = true
		case "omitempty":
			fi.omitempty = true
		case "remain":
			fi.remain = true
		}
	}
	return fi
}

func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// isLeafStruct reports whether a struct type is treated as an atomic value.
func isLeafStruct(t reflect.Type) bool {
	return t == timeType ||
		t.Implements(textMarshalerType) || reflect.PtrTo(t).Implements(textMarshalerType) ||
		reflect.PtrTo(t).Implements(textUnmarshalerType)
}

// tagEntry maps a path (relative to the binding) to a tag value.
type tagEntry struct {
	path []string
	val  string
}

// collectTags gathers `default` and `env` tags of the leaf fields of t.
func collectTags(t reflect.Type) (defaults, envs []tagEntry) {
	walkLeaves(t, nil, map[reflect.Type]bool{}, func(sf reflect.StructField, path []string) {
		if d, ok := sf.Tag.Lookup("default"); ok {
			defaults = append(defaults, tagEntry{path: path, val: d})
		}
		if e := sf.Tag.Get("env"); e != "" {
			envs = append(envs, tagEntry{path: path, val: e})
		}
	})
	return
}

func walkLeaves(t reflect.Type, path []string, stack map[reflect.Type]bool, visit func(reflect.StructField, []string)) {
	t = derefType(t)
	if t.Kind() != reflect.Struct || isLeafStruct(t) || stack[t] {
		return
	}
	stack[t] = true
	defer delete(stack, t)
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fi := parseField(sf)
		if fi.skip || fi.remain {
			continue
		}
		ft := derefType(sf.Type)
		if fi.squash && ft.Kind() == reflect.Struct {
			walkLeaves(ft, path, stack, visit)
			continue
		}
		p := joinPath(path, fi.name)
		if ft.Kind() == reflect.Struct && !isLeafStruct(ft) {
			walkLeaves(ft, p, stack, visit)
			continue
		}
		visit(sf, p)
	}
}
