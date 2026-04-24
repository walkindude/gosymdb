package typerefs

import "example.com/samplemod/iface"

type EmbedDoer struct {
	iface.RealDoer // embed
}

func UseTypeAssert(v interface{}) {
	_ = v.(*iface.RealDoer) // type_assert
}

func UseTypeSwitch(v interface{}) {
	switch v.(type) {
	case *iface.RealDoer: // type_switch
	}
}

func UseCompositeLit() *iface.RealDoer {
	return &iface.RealDoer{} // composite_lit
}
