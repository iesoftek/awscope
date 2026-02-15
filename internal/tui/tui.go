package tui

import (
	"context"

	"awscope/internal/store"
	"awscope/internal/tui/app"
)

type Options struct {
	Profile string
	Icons   string
}

func Run(ctx context.Context, st *store.Store, opts Options) error {
	return app.Run(ctx, st, app.Options{Profile: opts.Profile, Icons: opts.Icons})
}
