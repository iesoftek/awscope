package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"awscope/internal/aws"
	"awscope/internal/core"
	"awscope/internal/providers/registry"
	"awscope/internal/scanui"
	"awscope/internal/store"

	"github.com/spf13/cobra"
)

func newScanCmd(dbPath *string, offline *bool) *cobra.Command {
	var (
		profile                      string
		regions                      string
		services                     string
		noCloudTrail                 bool
		plain                        bool
		concurrency                  int
		resolverConcurrency          int
		auditRegionConcurrency       int
		auditSourceConcurrency       int
		auditLookupIntervalMS        int
		elbv2TargetHealthConcurrency int
		costConcurrency              int
		targetSeconds                int
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
			if noCloudTrail {
				filtered := make([]string, 0, len(serviceIDs))
				for _, sid := range serviceIDs {
					if sid == "cloudtrail" {
						continue
					}
					filtered = append(filtered, sid)
				}
				serviceIDs = filtered
			}
			if len(serviceIDs) == 0 {
				return fmt.Errorf("no services/providers selected after filtering")
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
					Profile:                      profile,
					Regions:                      regionList,
					ProviderIDs:                  serviceIDs,
					MaxConcurrency:               concurrency,
					ResolverConcurrency:          resolverConcurrency,
					AuditRegionConcurrency:       auditRegionConcurrency,
					AuditSourceConcurrency:       auditSourceConcurrency,
					AuditLookupInterval:          time.Duration(auditLookupIntervalMS) * time.Millisecond,
					ELBv2TargetHealthConcurrency: elbv2TargetHealthConcurrency,
					CostConcurrency:              costConcurrency,
					TargetDuration:               time.Duration(targetSeconds) * time.Second,
				})
			} else {
				res, err = scanui.Run(runCtx, st, scanui.Options{
					Profile:                      profile,
					Regions:                      regionList,
					ProviderIDs:                  serviceIDs,
					MaxConcurrency:               concurrency,
					ResolverConcurrency:          resolverConcurrency,
					AuditRegionConcurrency:       auditRegionConcurrency,
					AuditSourceConcurrency:       auditSourceConcurrency,
					AuditLookupInterval:          time.Duration(auditLookupIntervalMS) * time.Millisecond,
					ELBv2TargetHealthConcurrency: elbv2TargetHealthConcurrency,
					CostConcurrency:              costConcurrency,
					TargetDuration:               time.Duration(targetSeconds) * time.Second,
				})
			}
			if err != nil {
				return err
			}

			fmt.Printf("scan complete: account=%s partition=%s resources=%d edges=%d db=%s\n",
				res.AccountID, res.Partition, res.Resources, res.Edges, st.DBPath())
			fmt.Println(formatDetailedScanSummary(res))
			fmt.Println(formatScanPerformanceSummary(res))
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
	cmd.Flags().BoolVar(&noCloudTrail, "no-cloudtrail", false, "Exclude CloudTrail provider/event indexing from scan")
	servicesUsage := "Comma-separated service/provider IDs to scan (default: all supported)"
	if known := registry.ListIDs(); len(known) > 0 {
		sort.Strings(known)
		servicesUsage += fmt.Sprintf(" (supported: %s)", strings.Join(known, ","))
	}
	cmd.Flags().StringVar(&services, "services", "", servicesUsage)
	cmd.Flags().BoolVar(&plain, "plain", false, "Disable progress UI and print only the final summary")
	cmd.Flags().IntVar(&concurrency, "concurrency", intEnvOr("AWSCOPE_SCAN_CONCURRENCY", 16), "Max concurrent AWS scan tasks")
	cmd.Flags().IntVar(&resolverConcurrency, "resolver-concurrency", intEnvOr("AWSCOPE_RESOLVER_CONCURRENCY", 8), "Max concurrent resolver tasks (ELBv2 membership)")
	cmd.Flags().IntVar(&auditRegionConcurrency, "audit-region-concurrency", intEnvOr("AWSCOPE_AUDIT_REGION_CONCURRENCY", 10), "Max concurrent audit region workers")
	cmd.Flags().IntVar(&auditSourceConcurrency, "audit-source-concurrency", intEnvOr("AWSCOPE_AUDIT_SOURCE_CONCURRENCY", 3), "Max concurrent CloudTrail event-source workers per region")
	cmd.Flags().IntVar(&auditLookupIntervalMS, "audit-lookup-interval-ms", intEnvOr("AWSCOPE_AUDIT_LOOKUP_INTERVAL_MS", 0), "Delay between CloudTrail LookupEvents requests in milliseconds (0 disables pacing delay)")
	cmd.Flags().IntVar(&elbv2TargetHealthConcurrency, "elbv2-targethealth-concurrency", intEnvOr("AWSCOPE_ELBV2_TARGETHEALTH_CONCURRENCY", 30), "Max concurrent ELBv2 target health calls per resolver region")
	cmd.Flags().IntVar(&costConcurrency, "cost-concurrency", intEnvOr("AWSCOPE_COST_CONCURRENCY", 16), "Max concurrent cost estimation workers")
	cmd.Flags().IntVar(&targetSeconds, "target-seconds", intEnvOr("AWSCOPE_SCAN_TARGET_SECONDS", 60), "Scan target duration in seconds (reporting only)")
	return cmd
}
