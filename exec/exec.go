package exec

import (
	"context"
	"os"
	"os/exec"

	"github.com/bartdeboer/go-kernel"
)

const AdapterID = "exec"

// Ensure Executor implements both roles.
var (
	_ kernel.Executor = (*Executor)(nil)
	_ CommandExecutor = (*Executor)(nil)
)

// The Executor adapter is a simple provider for running commands
// locally on the host using os/exec.
type Executor struct{}

// Register the adapter with the core registry.
func init() {
	kernel.Register(AdapterID, func() kernel.Adapter {
		return &Executor{}
	})
}

// RunCommand implements CommandExecutor.
// It executes the given Command using os/exec.
func (e *Executor) RunCommand(ctx context.Context, cmd Command) error {
	if cmd.Name == "" {
		return nil
	}

	c := exec.CommandContext(ctx, cmd.Name, cmd.Args...)

	// Env & Dir
	if len(cmd.Env) > 0 {
		c.Env = cmd.Env
	}
	if cmd.Dir != "" {
		c.Dir = cmd.Dir
	}

	// IO wiring with sensible defaults.
	if cmd.Stdin != nil {
		c.Stdin = cmd.Stdin
	} else {
		c.Stdin = os.Stdin
	}
	if cmd.Stdout != nil {
		c.Stdout = cmd.Stdout
	} else {
		c.Stdout = os.Stdout
	}
	if cmd.Stderr != nil {
		c.Stderr = cmd.Stderr
	} else {
		c.Stderr = os.Stderr
	}

	return c.Run()
}

func (e *Executor) Run(ctx context.Context, name string, args ...string) error {
	return Run(ctx, e, name, args...)
}

func (e *Executor) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return Output(ctx, e, name, args...)
}
