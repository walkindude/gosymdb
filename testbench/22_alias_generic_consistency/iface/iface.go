package iface

type Reader interface {
	Read([]byte) (int, error)
}

type Box[T any] interface {
	Unbox() T
}
