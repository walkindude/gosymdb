package beta

import "example.com/samplemod/alpha"

// RegisterHook registers a callback-style function value — tests func-ref detection.
func RegisterHook(fn func(s *alpha.Store) string) string {
	return fn(&alpha.Store{})
}

// SetupWithCallback passes alpha.Top as a function value argument.
func SetupWithCallback() string {
	return RegisterHook(alpha.Top)
}
