package app

import (
	"sync"
)

type SlaveCollector struct {
	Lock       sync.RWMutex
	Collectors map[string]struct{}
}

func (s *SlaveCollector) Range(f func(addr string) error) (err error) {
	for addr, _ := range s.Collectors {
		err = f(addr)
		if err != nil {
			return
		}
	}
	return
}
func (l *SlaveCollector) Exists(key string) bool {
	l.Lock.RLock()
	defer l.Lock.RUnlock()
	_, ok := l.Collectors[key]
	return ok
}
func (l *SlaveCollector) ToSlice() (res []string) {
	l.Lock.RLock()
	defer l.Lock.RUnlock()
	for v, _ := range l.Collectors {
		res = append(res, v)
	}
	return res
}
func (l *SlaveCollector) Init(nodes []string) {
	l.Lock.Lock()
	defer l.Lock.Unlock()
	l.Collectors = make(map[string]struct{})
	for _, node := range nodes {
		l.Collectors[node] = struct{}{}
	}
	if CollectorappIdentity == "master" {
		pushSlaveSignal <- "all"
	}
	return
}
func (l *SlaveCollector) Set(key string) {
	l.Lock.Lock()
	defer l.Lock.Unlock()
	l.Collectors[key] = struct{}{}
	return
}
func (l *SlaveCollector) Del(key string) {
	l.Lock.Lock()
	defer l.Lock.Unlock()
	delete(l.Collectors, key)
	return
}
func (l *SlaveCollector) Len() (length int) {
	l.Lock.RLock()
	defer l.Lock.RUnlock()
	return len(l.Collectors)
}
