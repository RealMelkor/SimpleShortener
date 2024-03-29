package main

import (
	"sync"
	"errors"
)

type stack struct {
	lock sync.Mutex
	s []string
}

func NewStack() *stack {
	return &stack{sync.Mutex{}, make([]string, 0)}
}

func (s *stack) Push(v string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.s = append(s.s, v)
}

func (s *stack) Pop() (string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	l := len(s.s)
	if l == 0 {
		return "", errors.New("Empty Stack")
	}

	v := s.s[l - 1]
	s.s = s.s[:l - 1]
	return v, nil
}

func (s *stack) IsEmpty() bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	return len(s.s) == 0
}
