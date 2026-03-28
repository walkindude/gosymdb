package use

import (
	"testbench/alias_generic_consistency/alias"
	"testbench/alias_generic_consistency/iface"
)

func AcceptAlias(r alias.ReaderAlias) alias.ReaderAlias {
	return r
}

func MakeBoxInt(v int) iface.Box[int] {
	return alias.GenericBox[int]{V: v}
}

func UseReader(r iface.Reader, p []byte) (int, error) {
	return r.Read(p)
}

func UseBoxInt(v int) int {
	return MakeBoxInt(v).Unbox()
}
