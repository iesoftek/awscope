package registry

import (
	"fmt"
	"sort"
	"sync"

	"awscope/internal/providers"
)

var (
	mu        sync.RWMutex
	byID      = map[string]providers.Provider{}
	byOrderID []string
)

func Register(p providers.Provider) {
	mu.Lock()
	defer mu.Unlock()
	id := p.ID()
	if id == "" {
		panic("provider id must not be empty")
	}
	if _, exists := byID[id]; exists {
		panic(fmt.Sprintf("provider already registered: %s", id))
	}
	byID[id] = p
	byOrderID = append(byOrderID, id)
	sort.Strings(byOrderID)
}

func MustGet(id string) providers.Provider {
	p, ok := Get(id)
	if !ok {
		panic(fmt.Sprintf("unknown provider: %s", id))
	}
	return p
}

func Get(id string) (providers.Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := byID[id]
	return p, ok
}

func ListIDs() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(byOrderID))
	out = append(out, byOrderID...)
	return out
}
