package conic

import (
	"encoding"
	"fmt"
	"reflect"
)

// collector records the (relative) paths of struct fields that were omitted
// because of `omitempty` and a zero value.
type collector struct {
	omitted [][]string
}

var basicTypes = map[reflect.Kind]reflect.Type{
	reflect.Bool:    reflect.TypeOf(false),
	reflect.Int:     reflect.TypeOf(int(0)),
	reflect.Int8:    reflect.TypeOf(int8(0)),
	reflect.Int16:   reflect.TypeOf(int16(0)),
	reflect.Int32:   reflect.TypeOf(int32(0)),
	reflect.Int64:   reflect.TypeOf(int64(0)),
	reflect.Uint:    reflect.TypeOf(uint(0)),
	reflect.Uint8:   reflect.TypeOf(uint8(0)),
	reflect.Uint16:  reflect.TypeOf(uint16(0)),
	reflect.Uint32:  reflect.TypeOf(uint32(0)),
	reflect.Uint64:  reflect.TypeOf(uint64(0)),
	reflect.Uintptr: reflect.TypeOf(uintptr(0)),
	reflect.Float32: reflect.TypeOf(float32(0)),
	reflect.Float64: reflect.TypeOf(float64(0)),
	reflect.String:  reflect.TypeOf(""),
}

// toPlain converts v into a tree of plain values (map[string]any, []any and
// basic types) following mapstructure's rules.
func toPlain(v reflect.Value, col *collector) (any, bool) {
	return plainValue(v, col, nil)
}

func plainValue(v reflect.Value, col *collector, path []string) (any, bool) {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr) {
		if v.IsNil() {
			return nil, false
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return nil, false
	}
	t := v.Type()
	if t == durationType {
		return v.Interface().(interface{ String() string }).String(), true
	}
	if m, ok := asTextMarshaler(v); ok {
		if b, err := m.MarshalText(); err == nil {
			return string(b), true
		}
	}
	switch v.Kind() {
	case reflect.Struct:
		out := map[string]any{}
		fillStruct(v, out, col, path)
		return out, true
	case reflect.Map:
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			k := fmt.Sprint(iter.Key().Interface())
			e, ok := plainValue(iter.Value(), col, joinPath(path, k))
			if !ok {
				e = nil
			}
			out[k] = e
		}
		return out, true
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, v.Len())
		for i := 0; i < v.Len(); i++ {
			e, ok := plainValue(v.Index(i), col, nil)
			if !ok {
				e = nil
			}
			out = append(out, e)
		}
		return out, true
	}
	bt, ok := basicTypes[v.Kind()]
	if !ok {
		return nil, false
	}
	if t == bt {
		return v.Interface(), true
	}
	return v.Convert(bt).Interface(), true
}

func asTextMarshaler(v reflect.Value) (encoding.TextMarshaler, bool) {
	t := v.Type()
	if t.Implements(textMarshalerType) {
		if v.CanInterface() {
			m, ok := v.Interface().(encoding.TextMarshaler)
			return m, ok
		}
		return nil, false
	}
	if reflect.PtrTo(t).Implements(textMarshalerType) {
		p := reflect.New(t)
		p.Elem().Set(v)
		m, ok := p.Interface().(encoding.TextMarshaler)
		return m, ok
	}
	return nil, false
}

func fillStruct(v reflect.Value, out map[string]any, col *collector, path []string) {
	t := v.Type()
	var remains []reflect.Value
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fi := parseField(sf)
		if fi.skip {
			continue
		}
		fv := v.Field(i)
		if fi.squash {
			sv := fv
			for sv.Kind() == reflect.Ptr && !sv.IsNil() {
				sv = sv.Elem()
			}
			if sv.Kind() == reflect.Struct {
				fillStruct(sv, out, col, path)
				continue
			}
		}
		if fi.remain {
			if fv.Kind() == reflect.Map {
				remains = append(remains, fv)
			}
			continue
		}
		p := joinPath(path, fi.name)
		if fi.omitempty && fv.IsZero() {
			if col != nil {
				col.omitted = append(col.omitted, p)
			}
			continue
		}
		if val, ok := plainValue(fv, col, p); ok {
			out[fi.name] = val
		}
	}
	for _, rm := range remains {
		iter := rm.MapRange()
		for iter.Next() {
			k := fmt.Sprint(iter.Key().Interface())
			if _, exists := out[k]; exists {
				continue
			}
			e, ok := plainValue(iter.Value(), col, joinPath(path, k))
			if !ok {
				e = nil
			}
			out[k] = e
		}
	}
}

// zeroMapped zeroes every field of the struct v that takes part in decoding,
// keeping fields mapstructure ignores (unexported or tagged "-").
func zeroMapped(v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		fi := parseField(t.Field(i))
		if fi.skip {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		if fv.Kind() == reflect.Struct && !isLeafStruct(fv.Type()) && (fi.squash || !fi.remain) {
			zeroMapped(fv)
			continue
		}
		fv.Set(reflect.Zero(fv.Type()))
	}
}
