package security

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case *string:
		if x == nil {
			return ""
		}
		return strings.TrimSpace(*x)
	case []byte:
		return strings.TrimSpace(string(x))
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func asBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case *bool:
		if x == nil {
			return false, false
		}
		return *x, true
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		switch s {
		case "true", "1", "yes", "y":
			return true, true
		case "false", "0", "no", "n":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int8:
		return int(x), true
	case int16:
		return int(x), true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case uint:
		return int(x), true
	case uint8:
		return int(x), true
	case uint16:
		return int(x), true
	case uint32:
		return int(x), true
	case uint64:
		if x > math.MaxInt {
			return 0, false
		}
		return int(x), true
	case float32:
		return int(x), true
	case float64:
		return int(x), true
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i), true
		}
		if f, err := x.Float64(); err == nil {
			return int(f), true
		}
		return 0, false
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		if i, err := strconv.Atoi(s); err == nil {
			return i, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f), true
		}
		return 0, false
	default:
		return 0, false
	}
}

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if v == nil {
		return nil
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func mapValue(m map[string]any, keys ...string) any {
	if len(m) == 0 || len(keys) == 0 {
		return nil
	}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	for mk, mv := range m {
		for _, k := range keys {
			if strings.EqualFold(mk, k) {
				return mv
			}
		}
	}
	return nil
}
