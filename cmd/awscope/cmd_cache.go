package main

import (
	"fmt"

	"awscope/internal/store"

	"github.com/spf13/cobra"
)

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Cache maintenance commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCacheStatsCmd())
	return cmd
}

func newCacheStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Print cache/database stats",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()
			dbPath := mustRootFlag(cmd, "db-path")
			st, err := store.Open(store.OpenOptions{Path: dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			rCount, err := st.CountResources(runCtx)
			if err != nil {
				return err
			}
			eCount, err := st.CountEdges(runCtx)
			if err != nil {
				return err
			}
			fmt.Printf("db: %s\nresources: %d\nedges: %d\n", st.DBPath(), rCount, eCount)

			rows, err := st.CountResourcesByType(runCtx)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Printf("- %s (%s): %d\n", r.Type, r.Service, r.Count)
			}
			return nil
		},
	}
}

func mustRootFlag(cmd *cobra.Command, name string) string {
	if cmd == nil || cmd.Root() == nil {
		return ""
	}
	f := cmd.Root().PersistentFlags().Lookup(name)
	if f == nil {
		return ""
	}
	return f.Value.String()
}
