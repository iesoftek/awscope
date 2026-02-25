package main

import (
	"fmt"
	"os"

	actionsRegistry "awscope/internal/actions/registry"
	"awscope/internal/core"
	"awscope/internal/graph"
	"awscope/internal/store"

	"github.com/spf13/cobra"
)

func newActionCmd(dbPath *string, offline *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "action",
		Short: "Run operational actions with audit logging",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newActionListCmd())
	cmd.AddCommand(newActionRunCmd(dbPath, offline))
	return cmd
}

func newActionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available action IDs",
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, id := range actionsRegistry.ListIDs() {
				fmt.Println(id)
			}
			return nil
		},
	}
}

func newActionRunCmd(dbPath *string, offline *bool) *cobra.Command {
	var (
		actionID string
		key      string
		profile  string
		confirm  bool
	)
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run an action against a specific resource_key",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()
			if offline != nil && *offline {
				return fmt.Errorf("--offline is not compatible with action run")
			}
			if actionID == "" {
				return fmt.Errorf("--id is required")
			}
			if key == "" {
				return fmt.Errorf("--key is required (resource_key from DB/export)")
			}
			if !confirm {
				return fmt.Errorf("refusing to run without --confirm")
			}

			a, ok := actionsRegistry.Get(actionID)
			if !ok {
				return fmt.Errorf("unknown action %q (known: %v)", actionID, actionsRegistry.ListIDs())
			}

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			profileName := effectiveProfile(profile)

			// Validate action exists (keeps error message close to CLI input).
			_ = a

			res, err := core.RunAction(runCtx, st, actionID, graph.ResourceKey(key), profileName)
			if err != nil {
				return err
			}
			fmt.Printf("action complete: id=%s run_id=%s status=%s\n", res.ActionID, res.ActionRunID, res.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&actionID, "id", "", "Action ID (e.g. ec2.stop)")
	cmd.Flags().StringVar(&key, "key", "", "Resource key string")
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (defaults to AWS_PROFILE or 'default')")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Acknowledge this may change AWS resources")
	return cmd
}

func effectiveProfile(in string) string {
	if in != "" {
		return in
	}
	if env := os.Getenv("AWS_PROFILE"); env != "" {
		return env
	}
	return "default"
}
