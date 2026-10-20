package store

import "testing"

func TestMemory(t *testing.T) {
	RunContract(t, func(t *testing.T) Store { return NewMemory() })
}
