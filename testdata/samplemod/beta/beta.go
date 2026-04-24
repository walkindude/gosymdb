package beta

import "example.com/samplemod/alpha"

var Hooks = struct {
	Run func() string
}{
	Run: func() string {
		s := &alpha.Store{}
		return alpha.Top(s)
	},
}

func Use() string {
	s := &alpha.Store{}
	return alpha.Top(s)
}
