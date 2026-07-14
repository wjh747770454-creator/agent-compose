package sessions

import (
	"strings"
	"sync"
)

// LifecycleLocks serializes state-changing operations for a sandbox within a
// daemon process. The lifecycle journal remains the durable crash-recovery
// boundary; these locks close the live remove/resume/start race.
type LifecycleLocks struct {
	locks sync.Map
}

func NewLifecycleLocks() *LifecycleLocks {
	return &LifecycleLocks{}
}

func (l *LifecycleLocks) Lock(sandboxID string) func() {
	if l == nil {
		return func() {}
	}
	value, _ := l.locks.LoadOrStore(strings.TrimSpace(sandboxID), &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
