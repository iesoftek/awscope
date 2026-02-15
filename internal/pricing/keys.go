package pricing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

func canonicalFiltersJSON(filters map[string]string) (string, error) {
	if filters == nil {
		return "{}", nil
	}
	// Canonicalize by sorting keys and encoding a stable object.
	keys := make([]string, 0, len(filters))
	for k := range filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	obj := make(map[string]string, len(filters))
	for _, k := range keys {
		obj[k] = filters[k]
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func cacheKey(parts ...string) string {
	h := sha256.New()
	for i := range parts {
		h.Write([]byte(parts[i]))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)
}
