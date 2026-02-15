package registry

import (
	"fmt"
	"sort"
	"sync"

	"awscope/internal/actions"
)

var (
	mu        sync.RWMutex
	byID      = map[string]actions.Action{}
	byOrderID []string
)

func Register(a actions.Action) {
	mu.Lock()
	defer mu.Unlock()
	id := a.ID()
	if id == "" {
		panic("action id must not be empty")
	}
	if _, exists := byID[id]; exists {
		panic(fmt.Sprintf("action already registered: %s", id))
	}
	byID[id] = a
	byOrderID = append(byOrderID, id)
	sort.Strings(byOrderID)
}

func Get(id string) (actions.Action, bool) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := byID[id]
	return a, ok
}

func MustGet(id string) actions.Action {
	a, ok := Get(id)
	if !ok {
		panic(fmt.Sprintf("unknown action: %s", id))
	}
	return a
}

func ListIDs() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(byOrderID))
	out = append(out, byOrderID...)
	return out
}
