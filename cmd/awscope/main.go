package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	ctx := context.Background()

	var (
		dbPath  string
		offline bool
	)

	root := newRootCommand(ctx, &dbPath, &offline)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
