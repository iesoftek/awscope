package main

import (
	"fmt"
	"sort"
	"strings"

	"awscope/internal/aws"
	"awscope/internal/core"
	"awscope/internal/providers/registry"
	"awscope/internal/scanui"
	"awscope/internal/store"

	"github.com/spf13/cobra"
)

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
				// Default to scanning all registered providers.
				serviceIDs = registry.ListIDs()
				sort.Strings(serviceIDs)
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
	servicesUsage := "Comma-separated service/provider IDs to scan (default: all supported)"
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
