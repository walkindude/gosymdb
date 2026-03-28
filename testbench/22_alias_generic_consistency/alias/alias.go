package alias

import "testbench/alias_generic_consistency/iface"

type ReaderAlias = iface.Reader

type File struct{}

func (File) Read(p []byte) (int, error) { return len(p), nil }

type GenericBox[T any] struct {
	V T
}

func (b GenericBox[T]) Unbox() T { return b.V }
