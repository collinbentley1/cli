package cli

import (
	"context"

	"github.com/collinbentley1/cli/ot/config"
)

type runtimeKey struct{}

type Runtime struct {
	RepoRoot string
	Config   *config.Config
}

func withRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

func getRuntime(ctx context.Context) *Runtime {
	rt, _ := ctx.Value(runtimeKey{}).(*Runtime)
	return rt
}
