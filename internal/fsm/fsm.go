package fsm

import "sync"

type FSM struct {
	mu    sync.RWMutex
	state map[int64]State
}

func NewFSM() *FSM {
	return &FSM{state: map[int64]State{}}
}

func (f *FSM) Set(tgID int64, st State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state[tgID] = st
}

func (f *FSM) Get(tgID int64) State {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state[tgID]
}
