package main

import (
	"fmt"
	"strings"
	"time"

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
	cmd.AddCommand(newCachePruneStaleCmd())
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

func newCachePruneStaleCmd() *cobra.Command {
	var (
		profile string
		days    int
	)
	cmd := &cobra.Command{
		Use:   "prune-stale",
		Short: "Permanently delete stale resources and related edges/cost rows",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()
			dbPath := mustRootFlag(cmd, "db-path")
			st, err := store.Open(store.OpenOptions{Path: dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			accountID := ""
			profile = strings.TrimSpace(profile)
			if profile != "" {
				meta, ok, err := st.LookupProfile(runCtx, profile)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("unknown profile %q in DB (run `awscope scan --profile %s ...` first)", profile, profile)
				}
				accountID = strings.TrimSpace(meta.AccountID)
			}

			var olderThan *time.Time
			if days > 0 {
				cutoff := time.Now().UTC().AddDate(0, 0, -days)
				olderThan = &cutoff
			}

			res, edges, costs, err := st.PurgeStaleResources(runCtx, accountID, olderThan)
			if err != nil {
				return err
			}
			scope := "all accounts"
			if accountID != "" {
				scope = accountID
			}
			fmt.Printf("prune stale complete: scope=%s deleted_resources=%d deleted_edges=%d deleted_cost_rows=%d db=%s\n", scope, res, edges, costs, st.DBPath())
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (optional; prune stale rows only for this profile/account)")
	cmd.Flags().IntVar(&days, "days", 0, "Delete stale rows missing for at least N days (0 means all stale rows)")
	return cmd
}
