package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"

	actionsRegistry "awscope/internal/actions/registry"
	"awscope/internal/aws"
	"awscope/internal/core"
	"awscope/internal/graph"
	"awscope/internal/providers/registry"
	"awscope/internal/scanui"
	"awscope/internal/store"
	"awscope/internal/tui"

	_ "awscope/internal/actions/ec2"
	_ "awscope/internal/actions/ecs"

	_ "awscope/internal/providers/dynamodb"
	_ "awscope/internal/providers/ec2"
	_ "awscope/internal/providers/ecs"
	_ "awscope/internal/providers/elbv2"
	_ "awscope/internal/providers/iam"
	_ "awscope/internal/providers/kms"
	_ "awscope/internal/providers/lambda"
	_ "awscope/internal/providers/logs"
	_ "awscope/internal/providers/rds"
	_ "awscope/internal/providers/s3"
	_ "awscope/internal/providers/secretsmanager"
	_ "awscope/internal/providers/sns"
	_ "awscope/internal/providers/sqs"

	"github.com/spf13/cobra"
)

func main() {
	ctx := context.Background()

	var (
		dbPath  string
		offline bool
	)

	root := &cobra.Command{
		Use:           "awscope",
		Short:         "AWS resource browser TUI + inventory",
		SilenceErrors: true, // main() prints the error once for consistent formatting.
	}
	root.PersistentFlags().StringVar(&dbPath, "db-path", "", "Path to SQLite database (default: platform-specific user data dir)")
	root.PersistentFlags().BoolVar(&offline, "offline", false, "Disable AWS calls; browse cached inventory only")

	root.AddCommand(newTuiCmd(ctx, &dbPath, &offline))
	root.AddCommand(newVersionCmd())
	root.AddCommand(newScanCmd(&dbPath, &offline))
	root.AddCommand(newExportCmd(&dbPath))
	root.AddCommand(newCacheCmd())
	root.AddCommand(newActionCmd(&dbPath, &offline))

	// Default to TUI if no subcommand is provided.
	root.RunE = func(cmd *cobra.Command, args []string) error {
		return newTuiCmd(ctx, &dbPath, &offline).RunE(cmd, args)
	}

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newTuiCmd(ctx context.Context, dbPath *string, offline *bool) *cobra.Command {
	var profile string
	var iconsMode string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Run the interactive TUI",
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(store.OpenOptions{
				Path:    *dbPath,
				Offline: *offline,
			})
			if err != nil {
				return err
			}
			defer st.Close()

			return tui.Run(ctx, st, tui.Options{Profile: profile, Icons: iconsMode})
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (defaults to AWS_PROFILE or 'default')")
	cmd.Flags().StringVar(&iconsMode, "icons", "", "Icons mode: ascii|nerd|none (overrides AWSCOPE_ICONS; default: nerd)")
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			if info, ok := debug.ReadBuildInfo(); ok && info != nil {
				fmt.Printf("awscope %s\n", info.Main.Version)
				var (
					rev      string
					vcsTime  string
					modified string
				)
				for _, s := range info.Settings {
					switch s.Key {
					case "vcs.revision":
						rev = s.Value
					case "vcs.time":
						vcsTime = s.Value
					case "vcs.modified":
						modified = s.Value
					}
				}
				if rev != "" {
					fmt.Printf("revision: %s\n", rev)
				}
				if vcsTime != "" {
					fmt.Printf("time: %s\n", vcsTime)
				}
				if modified != "" {
					fmt.Printf("modified: %s\n", modified)
				}
				if info.GoVersion != "" {
					fmt.Printf("go: %s\n", info.GoVersion)
				}
				return nil
			}
			fmt.Println("awscope dev")
			return nil
		},
	}
}

func newScanCmd(dbPath *string, offline *bool) *cobra.Command {
	var (
		profile             string
		regions             string
		services            string
		plain               bool
		concurrency         int
		resolverConcurrency int
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan AWS and populate the local inventory",
		// For runtime scan errors (e.g. AccessDenied), don't spam help text.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()

			if offline != nil && *offline {
				return fmt.Errorf("--offline is not compatible with scan")
			}

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			regionList := parseCSV(regions)
			if len(regionList) == 0 {
				return fmt.Errorf("--regions is required (comma-separated, e.g. us-east-1,us-west-2, or 'all')")
			}
			if len(regionList) == 1 && strings.EqualFold(regionList[0], "all") {
				// Discover enabled regions using EC2 DescribeRegions.
				loader := aws.NewLoader()
				cfg, _, err := loader.Load(runCtx, profile, "us-east-1")
				if err != nil {
					return err
				}
				discovered, err := loader.ListEnabledRegions(runCtx, cfg)
				if err != nil {
					return fmt.Errorf("discover regions: %w (provide explicit --regions to bypass)", err)
				}
				regionList = discovered
			}

			serviceIDs := parseCSV(services)
			if len(serviceIDs) == 0 {
				// Default to EC2 for the first usable slice.
				serviceIDs = []string{"ec2"}
			}
			// Validate early for a clearer error.
			for _, sid := range serviceIDs {
				if _, ok := registry.Get(sid); !ok {
					return fmt.Errorf("unknown service/provider %q (known: %v)", sid, registry.ListIDs())
				}
			}

			app := core.New(st)
			var res core.ScanResult
			if plain {
				res, err = app.Scan(runCtx, core.ScanOptions{
					Profile:             profile,
					Regions:             regionList,
					ProviderIDs:         serviceIDs,
					MaxConcurrency:      concurrency,
					ResolverConcurrency: resolverConcurrency,
				})
			} else {
				res, err = scanui.Run(runCtx, st, scanui.Options{
					Profile:             profile,
					Regions:             regionList,
					ProviderIDs:         serviceIDs,
					MaxConcurrency:      concurrency,
					ResolverConcurrency: resolverConcurrency,
				})
			}
			if err != nil {
				return err
			}

			fmt.Printf("scan complete: account=%s partition=%s resources=%d edges=%d db=%s\n",
				res.AccountID, res.Partition, res.Resources, res.Edges, st.DBPath())
			if plain && len(res.StepFailures) > 0 {
				fmt.Printf("errors (%d):\n", len(res.StepFailures))
				for i, f := range res.StepFailures {
					if i >= 50 {
						fmt.Printf("  ... (+%d more)\n", len(res.StepFailures)-i)
						break
					}
					label := strings.TrimSpace(fmt.Sprintf("%s %s %s", f.Phase, f.ProviderID, f.Region))
					fmt.Printf("  - %-32s  %s\n", label, strings.TrimSpace(f.Error))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "AWS profile name (uses default chain if empty)")
	cmd.Flags().StringVar(&regions, "regions", "", "Comma-separated regions to scan (required; supports 'all')")
	servicesUsage := "Comma-separated service/provider IDs to scan (default: ec2)"
	if known := registry.ListIDs(); len(known) > 0 {
		sort.Strings(known)
		servicesUsage += fmt.Sprintf(" (supported: %s)", strings.Join(known, ","))
	}
	cmd.Flags().StringVar(&services, "services", "", servicesUsage)
	cmd.Flags().BoolVar(&plain, "plain", false, "Disable progress UI and print only the final summary")
	cmd.Flags().IntVar(&concurrency, "concurrency", 8, "Max concurrent AWS scan tasks")
	cmd.Flags().IntVar(&resolverConcurrency, "resolver-concurrency", 4, "Max concurrent resolver tasks (ELBv2 membership)")
	return cmd
}

func parseCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func newExportCmd(dbPath *string) *cobra.Command {
	var (
		format string
		out    string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export inventory from SQLite",
		RunE: func(cmd *cobra.Command, args []string) error {
			runCtx := cmd.Context()
			if out == "" {
				return fmt.Errorf("--out is required")
			}

			st, err := store.Open(store.OpenOptions{Path: *dbPath})
			if err != nil {
				return err
			}
			defer st.Close()

			switch format {
			case "json":
				return exportJSON(runCtx, st, out)
			default:
				return fmt.Errorf("unsupported --format %q (supported: json)", format)
			}
		},
	}
	cmd.Flags().StringVar(&format, "format", "json", "Export format (json)")
	cmd.Flags().StringVar(&out, "out", "", "Output file path (required)")
	return cmd
}

func exportJSON(ctx context.Context, st *store.Store, outPath string) error {
	snap, err := st.ExportLatest(ctx)
	if err != nil {
		return err
	}
	return store.WriteJSONFile(outPath, snap)
}

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
