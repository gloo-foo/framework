package gloo

// Switch is the interface for flag types.
// All flag types should implement this interface to configure the `flags` struct.
type Switch[T any] interface {
	Configure(*T)
}

// configure applies all switches to create a configured flags struct
func configure[T any](opts ...Switch[T]) T {
	def := new(T)
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt.Configure(def)
	}
	return *def
}
