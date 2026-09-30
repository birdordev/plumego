package pubsub

import "reflect"

// maxCloneDepth is the maximum recursion depth for cloneValue.
// Structs or pointer chains deeper than this limit are returned as-is (shallow
// copy) to prevent stack overflows on cyclic or deeply-nested data.
const maxCloneDepth = 32

// Cloner is an interface for types that can clone themselves.
type Cloner interface {
	Clone() any
}

// DeepCopier is an interface for types that can deep copy themselves.
type DeepCopier interface {
	DeepCopy() any
}

// cloneMessage creates a defensive copy of a message.
func cloneMessage(msg Message) Message {
	if msg.Meta != nil {
		meta := make(map[string]string, len(msg.Meta))
		for k, v := range msg.Meta {
			meta[k] = v
		}
		msg.Meta = meta
	}
	msg.Data = cloneData(msg.Data)
	return msg
}

// cloneData creates a deep copy of the data payload.
// It uses fast paths for common types before falling back to reflection.
func cloneData(data any) any {
	if data == nil {
		return nil
	}

	// Fast path: check interfaces first
	if copier, ok := data.(DeepCopier); ok {
		return copier.DeepCopy()
	}
	if copier, ok := data.(Cloner); ok {
		return copier.Clone()
	}

	// Fast path: common immutable types (no copy needed)
	switch data.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, complex64, complex128:
		return data
	}

	// Fast path: common reference types with type assertions
	switch v := data.(type) {
	case []byte:
		if v == nil {
			return nil
		}
		cp := make([]byte, len(v))
		copy(cp, v)
		return cp
	case []string:
		if v == nil {
			return nil
		}
		cp := make([]string, len(v))
		copy(cp, v)
		return cp
	case []int:
		if v == nil {
			return nil
		}
		cp := make([]int, len(v))
		copy(cp, v)
		return cp
	case []int64:
		if v == nil {
			return nil
		}
		cp := make([]int64, len(v))
		copy(cp, v)
		return cp
	case []float64:
		if v == nil {
			return nil
		}
		cp := make([]float64, len(v))
		copy(cp, v)
		return cp
	case []any:
		if v == nil {
			return nil
		}
		cp := make([]any, len(v))
		for i, elem := range v {
			cp[i] = cloneData(elem)
		}
		return cp
	case map[string]any:
		if v == nil {
			return nil
		}
		cp := make(map[string]any, len(v))
		for k, val := range v {
			cp[k] = cloneData(val)
		}
		return cp
	case map[string]string:
		if v == nil {
			return nil
		}
		cp := make(map[string]string, len(v))
		for k, val := range v {
			cp[k] = val
		}
		return cp
	case map[string]int:
		if v == nil {
			return nil
		}
		cp := make(map[string]int, len(v))
		for k, val := range v {
			cp[k] = val
		}
		return cp
	case map[string]int64:
		if v == nil {
			return nil
		}
		cp := make(map[string]int64, len(v))
		for k, val := range v {
			cp[k] = val
		}
		return cp
	case map[string]float64:
		if v == nil {
			return nil
		}
		cp := make(map[string]float64, len(v))
		for k, val := range v {
			cp[k] = val
		}
		return cp
	case map[string]bool:
		if v == nil {
			return nil
		}
		cp := make(map[string]bool, len(v))
		for k, val := range v {
			cp[k] = val
		}
		return cp
	case map[int]any:
		if v == nil {
			return nil
		}
		cp := make(map[int]any, len(v))
		for k, val := range v {
			cp[k] = cloneData(val)
		}
		return cp
	case map[int]string:
		if v == nil {
			return nil
		}
		cp := make(map[int]string, len(v))
		for k, val := range v {
			cp[k] = val
		}
		return cp
	}

	// Slow path: use reflection (depth starts at 0)
	cloned, ok := cloneValue(reflect.ValueOf(data), 0)
	if ok && cloned.IsValid() {
		return cloned.Interface()
	}
	return data
}

// cloneValue creates a deep copy using reflection.
// depth tracks the current recursion level; when it reaches maxCloneDepth the
// original value is returned unchanged to prevent stack overflows on cyclic or
// deeply-nested data structures.
func cloneValue(v reflect.Value, depth int) (reflect.Value, bool) {
	if !v.IsValid() {
		return v, true
	}

	// Depth guard: return original to avoid stack overflow on cyclic structs.
	if depth >= maxCloneDepth {
		return v, true
	}

	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v, true
		}
		cloned, _ := cloneValue(v.Elem(), depth+1)
		if !cloned.IsValid() {
			return v, true
		}
		out := reflect.New(v.Type()).Elem()
		if cloned.Type().AssignableTo(v.Type()) {
			out.Set(cloned)
			return out, true
		}
		if cloned.Type().ConvertibleTo(v.Type()) {
			out.Set(cloned.Convert(v.Type()))
			return out, true
		}
		out.Set(v.Elem())
		return out, true

	case reflect.Pointer:
		if v.IsNil() {
			return v, true
		}
		cloned, _ := cloneValue(v.Elem(), depth+1)
		if !cloned.IsValid() {
			return v, true
		}
		out := reflect.New(v.Elem().Type())
		if cloned.Type().AssignableTo(v.Elem().Type()) {
			out.Elem().Set(cloned)
			return out, true
		}
		if cloned.Type().ConvertibleTo(v.Elem().Type()) {
			out.Elem().Set(cloned.Convert(v.Elem().Type()))
			return out, true
		}
		out.Elem().Set(v.Elem())
		return out, true

	case reflect.Map:
		if v.IsNil() {
			return v, true
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key := iter.Key()
			value := iter.Value()
			clonedValue, _ := cloneValue(value, depth+1)
			if !clonedValue.IsValid() {
				clonedValue = value
			}
			if clonedValue.Type() != value.Type() {
				if clonedValue.Type().AssignableTo(value.Type()) {
					clonedValue = clonedValue.Convert(value.Type())
				} else if clonedValue.Type().ConvertibleTo(value.Type()) {
					clonedValue = clonedValue.Convert(value.Type())
				} else {
					clonedValue = value
				}
			}
			out.SetMapIndex(key, clonedValue)
		}
		return out, true

	case reflect.Slice:
		if v.IsNil() {
			return v, true
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		if v.Type().Elem().Kind() == reflect.Uint8 {
			reflect.Copy(out, v)
			return out, true
		}
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			clonedElem, _ := cloneValue(elem, depth+1)
			if !clonedElem.IsValid() {
				clonedElem = elem
			}
			if clonedElem.Type() != elem.Type() {
				if clonedElem.Type().AssignableTo(elem.Type()) {
					clonedElem = clonedElem.Convert(elem.Type())
				} else if clonedElem.Type().ConvertibleTo(elem.Type()) {
					clonedElem = clonedElem.Convert(elem.Type())
				} else {
					clonedElem = elem
				}
			}
			out.Index(i).Set(clonedElem)
		}
		return out, true

	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			elem := v.Index(i)
			clonedElem, _ := cloneValue(elem, depth+1)
			if !clonedElem.IsValid() {
				clonedElem = elem
			}
			if clonedElem.Type() != elem.Type() {
				if clonedElem.Type().AssignableTo(elem.Type()) {
					clonedElem = clonedElem.Convert(elem.Type())
				} else if clonedElem.Type().ConvertibleTo(elem.Type()) {
					clonedElem = clonedElem.Convert(elem.Type())
				} else {
					clonedElem = elem
				}
			}
			out.Index(i).Set(clonedElem)
		}
		return out, true

	case reflect.Struct:
		// Deep clone struct fields
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			outField := out.Field(i)

			// Skip unexported fields
			if !outField.CanSet() {
				continue
			}

			clonedField, _ := cloneValue(field, depth+1)
			if !clonedField.IsValid() {
				clonedField = field
			}
			if clonedField.Type() == outField.Type() {
				outField.Set(clonedField)
			} else if clonedField.Type().AssignableTo(outField.Type()) {
				outField.Set(clonedField)
			} else if clonedField.Type().ConvertibleTo(outField.Type()) {
				outField.Set(clonedField.Convert(outField.Type()))
			} else {
				outField.Set(field)
			}
		}
		return out, true

	default:
		return v, true
	}
}
