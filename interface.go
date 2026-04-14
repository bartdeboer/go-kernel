package kernel

import "context"

// Configurable is called with configurations that match the adapter ID
type Configurable interface {
	ConfigPtr() any
}

// ItemConfigurable is called for named configurations (name string)
// Adapters should capture the name here if they're needed.
// If both a named configuration and adapter configuration
// exists, both will be called.
type ItemConfigurable interface {
	ItemConfigPtr(name string) any
}

// Executor is a generic "do one unit of work" role.
// It is intentionally minimal so it can represent pipeline steps,
// tasks, jobs, commands, etc. in higher-level systems.
type Executor interface {
	Run(ctx context.Context, name string, args ...string) error
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type WorkDirSettable interface {
	SetWorkDir(hostPath string)
}

type Worker interface {
	InternalWorkDir() string
}

type Hydrater interface {
	Hydrate(ctx context.Context) error
}
