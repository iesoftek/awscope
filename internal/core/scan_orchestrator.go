package core

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"awscope/internal/audittrail"
	"awscope/internal/aws"
	"awscope/internal/catalog"
	"awscope/internal/cost"
	"awscope/internal/graph"
	"awscope/internal/pricing"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"
	"awscope/internal/store"

	"golang.org/x/sync/errgroup"
)

func (a *App) ScanWithProgress(ctx context.Context, opts ScanOptions, progress ScanProgressFn) (ScanResult, error) {
	if len(opts.Regions) == 0 {
		return ScanResult{}, fmt.Errorf("scan requires at least one region")
	}
	if len(opts.ProviderIDs) == 0 {
		return ScanResult{}, fmt.Errorf("scan requires at least one provider id")
	}

	cfg, id, err := a.loader.Load(ctx, opts.Profile, opts.Regions[0])
	if err != nil {
		return ScanResult{}, err
	}
	if err := aws.RequireIdentity(id); err != nil {
		return ScanResult{}, err
	}

	now := time.Now().UTC()
	profileName := opts.Profile
	if profileName == "" {
		if env := os.Getenv("AWS_PROFILE"); env != "" {
			profileName = env
		} else {
			profileName = "default"
		}
	}
	_ = a.store.UpsertAccountSeen(ctx, id.AccountID, id.Partition, now)
	_ = a.store.UpsertProfileUsed(ctx, profileName, id.AccountID, now)

	var (
		nodesSoFar int64
		edgesSoFar int64
	)

	// Total steps: each regional provider counts per region; each global provider counts once.
	auditRegions := make([]string, 0, len(opts.Regions))
	for _, r := range opts.Regions {
		r = strings.TrimSpace(r)
		if r == "" || strings.EqualFold(r, "global") {
			continue
		}
		auditRegions = append(auditRegions, r)
	}

	totalSteps := 0
	for _, pid := range opts.ProviderIDs {
		p := registry.MustGet(pid)
		if p.Scope() == providers.ScopeGlobal || p.Scope() == providers.ScopeAccount {
			totalSteps += 1
		} else {
			totalSteps += len(opts.Regions)
		}
	}
	needsResolver := has(opts.ProviderIDs, "ec2") && has(opts.ProviderIDs, "elbv2")
	needsAudit := has(opts.ProviderIDs, "cloudtrail") && len(auditRegions) > 0
	if needsResolver {
		totalSteps += len(opts.Regions)
	}
	if needsAudit {
		totalSteps += len(auditRegions)
	}
	// Cost indexing: one step per scanned provider (service).
	totalSteps += len(opts.ProviderIDs)
	completed := 0
	// "committed" is used to report CompletedSteps during concurrent execution.
	var committed int32

	// Collect target groups discovered during scan so we can resolve instance membership later.
	tgsByRegion := map[string][]string{}
	var failures []ScanStepFailure
	serviceCounts := map[string]int{}
	regionCounts := map[string]int{}

	stepSummary := func(providerID string, nodes []graph.ResourceNode, edges []graph.RelationshipEdge) (typeCounts map[string]int, sampleLabel string, sampleTotal int, sampleItems []string) {
		typeCounts = map[string]int{}
		for _, n := range nodes {
			if n.Type != "" {
				typeCounts[n.Type]++
			}
		}

		primaryType, sampleLabel := catalog.Sample(providerID)
		if primaryType == "" {
			return typeCounts, "", 0, nil
		}

		var names []string
		for _, n := range nodes {
			if n.Type != primaryType {
				continue
			}
			name := strings.TrimSpace(n.DisplayName)
			if name == "" {
				name = strings.TrimSpace(n.PrimaryID)
			}
			if name == "" {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)

		sampleTotal = len(names)
		const maxSample = 12
		if len(names) > maxSample {
			names = names[:maxSample]
		}
		return typeCounts, sampleLabel, sampleTotal, names
	}

	maxConc := opts.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 8
	}
	resolverConc := opts.ResolverConcurrency
	if resolverConc <= 0 {
		resolverConc = 4
	}

	type scanTask struct {
		phase      ScanProgressPhase
		providerID string
		stepRegion string
		startMsg   string
		reqRegions []string
		run        func(ctx context.Context) (providers.ListResult, error)
	}

	type scanTaskResult struct {
		task    scanTask
		res     providers.ListResult
		stepErr string
	}

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	resultsCh := make(chan scanTaskResult, maxConc*2)
	writerErrCh := make(chan error, 1)
	writerDone := make(chan struct{})

	// Single writer goroutine: serialize SQLite writes and keep counters consistent.
	go func() {
		defer close(writerDone)
		for r := range resultsCh {
			if r.stepErr != "" {
				failures = append(failures, ScanStepFailure{
					Phase:      r.task.phase,
					ProviderID: r.task.providerID,
					Region:     r.task.stepRegion,
					Error:      r.stepErr,
				})
				completed++
				atomic.StoreInt32(&committed, int32(completed))
				if progress != nil {
					progress(ScanProgressEvent{
						Phase:              r.task.phase,
						ProviderID:         r.task.providerID,
						Region:             r.task.stepRegion,
						Message:            "done",
						TotalSteps:         totalSteps,
						CompletedSteps:     completed,
						ResourcesSoFar:     int(atomic.LoadInt64(&nodesSoFar)),
						EdgesSoFar:         int(atomic.LoadInt64(&edgesSoFar)),
						StepResourcesAdded: 0,
						StepEdgesAdded:     0,
						StepTypeCounts:     map[string]int{},
						StepError:          r.stepErr,
					})
				}
				continue
			}

			if err := a.store.UpsertResources(ctx2, r.res.Nodes); err != nil {
				select {
				case writerErrCh <- err:
				default:
				}
				cancel()
				return
			}
			if err := a.store.UpsertEdges(ctx2, r.res.Edges); err != nil {
				select {
				case writerErrCh <- err:
				default:
				}
				cancel()
				return
			}

			atomic.AddInt64(&nodesSoFar, int64(len(r.res.Nodes)))
			atomic.AddInt64(&edgesSoFar, int64(len(r.res.Edges)))

			for _, n := range r.res.Nodes {
				svc := strings.TrimSpace(n.Service)
				if svc != "" {
					serviceCounts[svc]++
				}

				region := ""
				if _, _, rr, _, _, err := graph.ParseResourceKey(n.Key); err == nil {
					region = strings.TrimSpace(rr)
				}
				if region == "" {
					stepRegion := strings.TrimSpace(r.task.stepRegion)
					if stepRegion != "" && stepRegion != "account" {
						region = stepRegion
					}
				}
				if region != "" {
					regionCounts[region]++
				}
			}

			// Aggregate TGs for resolver (region derived from node key; ScopeAccount can span regions).
			if r.task.providerID == "elbv2" {
				for _, n := range r.res.Nodes {
					if n.Service != "elbv2" || n.Type != "elbv2:target-group" || n.PrimaryID == "" {
						continue
					}
					_, _, rr, _, _, err := graph.ParseResourceKey(n.Key)
					if err != nil || rr == "" {
						continue
					}
					tgsByRegion[rr] = append(tgsByRegion[rr], n.PrimaryID)
				}
			}

			typeCounts, sampleLabel, sampleTotal, sampleItems := stepSummary(r.task.providerID, r.res.Nodes, r.res.Edges)
			completed++
			atomic.StoreInt32(&committed, int32(completed))

			if progress != nil {
				progress(ScanProgressEvent{
					Phase:              r.task.phase,
					ProviderID:         r.task.providerID,
					Region:             r.task.stepRegion,
					Message:            "done",
					TotalSteps:         totalSteps,
					CompletedSteps:     completed,
					ResourcesSoFar:     int(atomic.LoadInt64(&nodesSoFar)),
					EdgesSoFar:         int(atomic.LoadInt64(&edgesSoFar)),
					StepResourcesAdded: len(r.res.Nodes),
					StepEdgesAdded:     len(r.res.Edges),
					StepTypeCounts:     typeCounts,
					StepSampleLabel:    sampleLabel,
					StepSampleTotal:    sampleTotal,
					StepSampleItems:    sampleItems,
				})
			}
		}
	}()

	// Build provider tasks.
	var tasks []scanTask
	for _, pid := range opts.ProviderIDs {
		p := registry.MustGet(pid)
		switch p.Scope() {
		case providers.ScopeGlobal:
			pid := pid
			p := p
			tasks = append(tasks, scanTask{
				phase:      PhaseProvider,
				providerID: pid,
				stepRegion: "global",
				startMsg:   "listing global resources",
				reqRegions: []string{"global"},
				run: func(ctx context.Context) (providers.ListResult, error) {
					return p.List(ctx, cfg, providers.ListRequest{
						Profile:   opts.Profile,
						AccountID: id.AccountID,
						Partition: id.Partition,
						Regions:   []string{"global"},
					})
				},
			})
		case providers.ScopeAccount:
			pid := pid
			p := p
			reqRegions := append([]string{}, opts.Regions...)
			tasks = append(tasks, scanTask{
				phase:      PhaseProvider,
				providerID: pid,
				stepRegion: "account",
				startMsg:   "listing account resources",
				reqRegions: reqRegions,
				run: func(ctx context.Context) (providers.ListResult, error) {
					return p.List(ctx, cfg, providers.ListRequest{
						Profile:   opts.Profile,
						AccountID: id.AccountID,
						Partition: id.Partition,
						Regions:   reqRegions,
					})
				},
			})
		default:
			for _, region := range opts.Regions {
				pid := pid
				p := p
				region := region
				tasks = append(tasks, scanTask{
					phase:      PhaseProvider,
					providerID: pid,
					stepRegion: region,
					startMsg:   "listing regional resources",
					reqRegions: []string{region},
					run: func(ctx context.Context) (providers.ListResult, error) {
						return p.List(ctx, cfg, providers.ListRequest{
							Profile:   opts.Profile,
							AccountID: id.AccountID,
							Partition: id.Partition,
							Regions:   []string{region},
						})
					},
				})
			}
		}
	}

	// Execute provider tasks concurrently (bounded), then close results channel so writer finishes.
	g, gctx := errgroup.WithContext(ctx2)
	taskCh := make(chan scanTask, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	for i := 0; i < maxConc; i++ {
		g.Go(func() error {
			for t := range taskCh {
				if progress != nil {
					progress(ScanProgressEvent{
						Phase:          t.phase,
						ProviderID:     t.providerID,
						Region:         t.stepRegion,
						Message:        t.startMsg,
						TotalSteps:     totalSteps,
						CompletedSteps: int(atomic.LoadInt32(&committed)),
						ResourcesSoFar: int(atomic.LoadInt64(&nodesSoFar)),
						EdgesSoFar:     int(atomic.LoadInt64(&edgesSoFar)),
					})
				}
				res, err := t.run(gctx)
				if err != nil {
					if isSkippableStepError(err) {
						select {
						case resultsCh <- scanTaskResult{task: t, stepErr: err.Error()}:
						case <-gctx.Done():
							return gctx.Err()
						}
						continue
					}
					cancel()
					return fmt.Errorf("%s/%s: %w", t.providerID, t.stepRegion, err)
				}
				select {
				case resultsCh <- scanTaskResult{task: t, res: res}:
				case <-gctx.Done():
					return gctx.Err()
				}
			}
			return nil
		})
	}

	workerErr := g.Wait()
	close(resultsCh)
	<-writerDone

	select {
	case err := <-writerErrCh:
		if err != nil {
			return ScanResult{}, err
		}
	default:
	}
	if workerErr != nil {
		return ScanResult{}, workerErr
	}

	// Resolver: instance -> target group membership (full mapping v1 slice).
	if needsResolver {
		type resolverResult struct {
			region string
			tgs    int
			edges  []graph.RelationshipEdge
			err    string
		}

		resCh := make(chan resolverResult, resolverConc*2)
		resDone := make(chan struct{})
		resErrCh := make(chan error, 1)

		go func() {
			defer close(resDone)
			for rr := range resCh {
				if rr.err != "" {
					failures = append(failures, ScanStepFailure{
						Phase:      PhaseResolver,
						ProviderID: "elbv2",
						Region:     rr.region,
						Error:      rr.err,
					})
					completed++
					atomic.StoreInt32(&committed, int32(completed))
					if progress != nil {
						progress(ScanProgressEvent{
							Phase:              PhaseResolver,
							ProviderID:         "elbv2",
							Region:             rr.region,
							Message:            "done",
							TotalSteps:         totalSteps,
							CompletedSteps:     completed,
							ResourcesSoFar:     int(atomic.LoadInt64(&nodesSoFar)),
							EdgesSoFar:         int(atomic.LoadInt64(&edgesSoFar)),
							StepResourcesAdded: 0,
							StepEdgesAdded:     0,
							StepSampleLabel:    "target groups",
							StepSampleTotal:    rr.tgs,
							StepError:          rr.err,
						})
					}
					continue
				}
				if err := a.store.UpsertEdges(ctx2, rr.edges); err != nil {
					select {
					case resErrCh <- err:
					default:
					}
					cancel()
					return
				}
				atomic.AddInt64(&edgesSoFar, int64(len(rr.edges)))
				completed++
				atomic.StoreInt32(&committed, int32(completed))
				if progress != nil {
					progress(ScanProgressEvent{
						Phase:              PhaseResolver,
						ProviderID:         "elbv2",
						Region:             rr.region,
						Message:            "done",
						TotalSteps:         totalSteps,
						CompletedSteps:     completed,
						ResourcesSoFar:     int(atomic.LoadInt64(&nodesSoFar)),
						EdgesSoFar:         int(atomic.LoadInt64(&edgesSoFar)),
						StepResourcesAdded: 0,
						StepEdgesAdded:     len(rr.edges),
						StepSampleLabel:    "target groups",
						StepSampleTotal:    rr.tgs,
					})
				}
			}
		}()

		rg, rctx := errgroup.WithContext(ctx2)
		regionCh := make(chan string, len(opts.Regions))
		for _, r := range opts.Regions {
			regionCh <- r
		}
		close(regionCh)

		for i := 0; i < min(resolverConc, len(opts.Regions)); i++ {
			rg.Go(func() error {
				for region := range regionCh {
					tgs := tgsByRegion[region]
					if progress != nil {
						progress(ScanProgressEvent{
							Phase:          PhaseResolver,
							ProviderID:     "elbv2",
							Region:         region,
							Message:        fmt.Sprintf("resolving instance target membership (tgs=%d)", len(tgs)),
							TotalSteps:     totalSteps,
							CompletedSteps: int(atomic.LoadInt32(&committed)),
							ResourcesSoFar: int(atomic.LoadInt64(&nodesSoFar)),
							EdgesSoFar:     int(atomic.LoadInt64(&edgesSoFar)),
						})
					}
					var refs []tgRef
					for _, arn := range tgs {
						refs = append(refs, tgRef{region: region, arn: arn})
					}
					edges, err := resolveInstanceTargetGroups(rctx, cfg, id.Partition, id.AccountID, refs)
					if err != nil {
						if isSkippableStepError(err) {
							// Best-effort skip resolver errors too (rare; depends on permissions).
							select {
							case resCh <- resolverResult{region: region, tgs: len(tgs), err: err.Error()}:
							case <-rctx.Done():
								return rctx.Err()
							}
							continue
						}
						cancel()
						return err
					}
					select {
					case resCh <- resolverResult{region: region, tgs: len(tgs), edges: edges}:
					case <-rctx.Done():
						return rctx.Err()
					}
				}
				return nil
			})
		}

		rerr := rg.Wait()
		close(resCh)
		<-resDone
		select {
		case err := <-resErrCh:
			if err != nil {
				return ScanResult{}, err
			}
		default:
		}
		if rerr != nil {
			return ScanResult{}, rerr
		}
	}

	// CloudTrail audit indexing: create/delete management events for high-impact services.
	// Best effort for access-denied regions; hard-fail on other errors.
	if needsAudit {
		const (
			auditWindowDays      = 7
			auditRetentionDays   = 30
			auditMaxEventsRegion = 1200
		)
		windowDays := envIntOr("AWSCOPE_AUDIT_WINDOW_DAYS", auditWindowDays)
		maxEventsRegion := envIntOr("AWSCOPE_AUDIT_MAX_EVENTS_PER_REGION", auditMaxEventsRegion)
		maxRegionDuration := envDurationSecondsOr("AWSCOPE_AUDIT_MAX_REGION_DURATION_SEC", 120*time.Second)

		lookupRegions := append([]string{}, auditRegions...)
		lookupRegions = append(lookupRegions, "global")
		lookups, err := a.store.ListResourceLookupsByAccountAndRegions(ctx2, id.AccountID, lookupRegions)
		if err != nil {
			return ScanResult{}, err
		}

		indexer := audittrail.NewIndexer(cfg, id.AccountID, id.Partition, lookups, audittrail.Options{
			WindowDays:         windowDays,
			MaxEventsPerRegion: maxEventsRegion,
			MaxRegionDuration:  maxRegionDuration,
			OnPage: func(p audittrail.PageProgress) {
				// Heartbeat to avoid "stuck" perception during long CloudTrail pagination.
				if progress == nil || p.Page%10 != 0 {
					return
				}
				progress(ScanProgressEvent{
					Phase:          PhaseAudit,
					ProviderID:     "cloudtrail",
					Region:         p.Region,
					Message:        fmt.Sprintf("indexing (src=%s page=%d indexed=%d)", p.Source, p.Page, p.Indexed),
					TotalSteps:     totalSteps,
					CompletedSteps: int(atomic.LoadInt32(&committed)),
					ResourcesSoFar: int(atomic.LoadInt64(&nodesSoFar)),
					EdgesSoFar:     int(atomic.LoadInt64(&edgesSoFar)),
				})
			},
		})

		type auditResult struct {
			region    string
			rows      []store.CloudTrailEventRow
			summary   audittrail.RegionSummary
			truncated bool
			stepErr   string
		}

		results := make(chan auditResult, len(auditRegions))
		auditConc := min(4, maxConc)
		if auditConc <= 0 {
			auditConc = 1
		}
		if auditConc > len(auditRegions) {
			auditConc = len(auditRegions)
		}

		ag, actx := errgroup.WithContext(ctx2)
		regionCh := make(chan string, len(auditRegions))
		for _, r := range auditRegions {
			regionCh <- r
		}
		close(regionCh)

		for i := 0; i < auditConc; i++ {
			ag.Go(func() error {
				for region := range regionCh {
					if progress != nil {
						progress(ScanProgressEvent{
							Phase:          PhaseAudit,
							ProviderID:     "cloudtrail",
							Region:         region,
							Message:        "indexing create/delete management events (7d)",
							TotalSteps:     totalSteps,
							CompletedSteps: int(atomic.LoadInt32(&committed)),
							ResourcesSoFar: int(atomic.LoadInt64(&nodesSoFar)),
							EdgesSoFar:     int(atomic.LoadInt64(&edgesSoFar)),
						})
					}

					regionRes, err := indexer.IndexRegion(actx, region)
					if err != nil {
						if audittrail.IsAccessDenied(err) || isEndpointUnavailable(err) {
							select {
							case results <- auditResult{region: region, stepErr: err.Error()}:
							case <-actx.Done():
								return actx.Err()
							}
							continue
						}
						cancel()
						return fmt.Errorf("cloudtrail/%s: %w", region, err)
					}

					select {
					case results <- auditResult{
						region:    region,
						rows:      regionRes.Rows,
						summary:   regionRes.Summary,
						truncated: regionRes.Truncated,
					}:
					case <-actx.Done():
						return actx.Err()
					}
				}
				return nil
			})
		}

		aerr := ag.Wait()
		close(results)
		if aerr != nil {
			return ScanResult{}, aerr
		}

		for rr := range results {
			stepErr := strings.TrimSpace(rr.stepErr)
			if stepErr != "" {
				failures = append(failures, ScanStepFailure{
					Phase:      PhaseAudit,
					ProviderID: "cloudtrail",
					Region:     rr.region,
					Error:      stepErr,
				})
				completed++
				atomic.StoreInt32(&committed, int32(completed))
				if progress != nil {
					progress(ScanProgressEvent{
						Phase:              PhaseAudit,
						ProviderID:         "cloudtrail",
						Region:             rr.region,
						Message:            "done",
						TotalSteps:         totalSteps,
						CompletedSteps:     completed,
						ResourcesSoFar:     int(atomic.LoadInt64(&nodesSoFar)),
						EdgesSoFar:         int(atomic.LoadInt64(&edgesSoFar)),
						StepResourcesAdded: 0,
						StepEdgesAdded:     0,
						StepTypeCounts:     map[string]int{},
						StepError:          stepErr,
					})
				}
				continue
			}

			if err := a.store.UpsertCloudTrailEvents(ctx2, rr.rows); err != nil {
				return ScanResult{}, err
			}

			stepCounts := map[string]int{
				"create": rr.summary.Create,
				"delete": rr.summary.Delete,
				"pages":  rr.summary.Pages,
			}
			msg := "done"
			if rr.truncated {
				msg = fmt.Sprintf("done (capped at %d events or duration)", maxEventsRegion)
			}

			completed++
			atomic.StoreInt32(&committed, int32(completed))
			if progress != nil {
				progress(ScanProgressEvent{
					Phase:              PhaseAudit,
					ProviderID:         "cloudtrail",
					Region:             rr.region,
					Message:            msg,
					TotalSteps:         totalSteps,
					CompletedSteps:     completed,
					ResourcesSoFar:     int(atomic.LoadInt64(&nodesSoFar)),
					EdgesSoFar:         int(atomic.LoadInt64(&edgesSoFar)),
					StepResourcesAdded: len(rr.rows),
					StepEdgesAdded:     0,
					StepTypeCounts:     stepCounts,
					StepSampleLabel:    "events",
					StepSampleTotal:    rr.summary.Indexed,
					StepSampleItems:    rr.summary.Samples,
				})
			}
		}

		cutoff := time.Now().UTC().AddDate(0, 0, -auditRetentionDays)
		if _, err := a.store.PruneCloudTrailEventsOlderThan(ctx2, id.AccountID, cutoff); err != nil {
			return ScanResult{}, err
		}
	}

	// Cost indexing: best-effort estimated monthly cost per resource.
	// This stage never fails the scan due to Pricing API errors; it records warnings in StepFailures.
	{
		// Include "global" for providers that emit global resources (e.g. IAM).
		regionsForCost := append([]string{}, opts.Regions...)
		regionsForCost = append(regionsForCost, "global")

		pc := pricing.NewClient(a.store, cfg, pricing.Options{})

		costConc := min(8, maxConc)
		if costConc <= 0 {
			costConc = 1
		}

		// If Pricing is denied once, disable further pricing calls and only write unknown rows.
		var pricingDenied int32
		var pricingDeniedRecorded int32

		for _, sid := range opts.ProviderIDs {
			sid := sid
			if progress != nil {
				progress(ScanProgressEvent{
					Phase:          PhaseCost,
					ProviderID:     sid,
					Region:         "all",
					Message:        "estimating monthly cost",
					TotalSteps:     totalSteps,
					CompletedSteps: int(atomic.LoadInt32(&committed)),
					ResourcesSoFar: int(atomic.LoadInt64(&nodesSoFar)),
					EdgesSoFar:     int(atomic.LoadInt64(&edgesSoFar)),
				})
			}

			targets, err := a.store.ListCostIndexTargets(ctx2, id.AccountID, sid, regionsForCost)
			if err != nil {
				return ScanResult{}, err
			}

			typeCounts := map[string]int{"priced": 0, "unknown": 0}
			rows := make([]store.ResourceCostRow, 0, len(targets))

			// Worker pool.
			type job struct{ t store.CostIndexTarget }
			jobs := make(chan job, costConc*2)

			var muRows sync.Mutex
			var wg sync.WaitGroup

			worker := func() {
				defer wg.Done()
				for j := range jobs {
					if ctx2.Err() != nil {
						return
					}
					var pcc *pricing.Client
					if atomic.LoadInt32(&pricingDenied) == 0 {
						pcc = pc
					}

					row, _, estErr := cost.Estimate(ctx2, j.t, pcc)
					if estErr != nil && isAccessDenied(estErr) {
						atomic.CompareAndSwapInt32(&pricingDenied, 0, 1)
					}

					muRows.Lock()
					rows = append(rows, row)
					if row.EstMonthlyUSD != nil {
						typeCounts["priced"]++
					} else {
						typeCounts["unknown"]++
					}
					muRows.Unlock()

				}
			}

			nw := costConc
			if len(targets) < nw {
				nw = len(targets)
			}
			if nw < 1 {
				nw = 1
			}
			wg.Add(nw)
			for i := 0; i < nw; i++ {
				go worker()
			}
			for _, t := range targets {
				jobs <- job{t: t}
			}
			close(jobs)
			wg.Wait()

			// Write rows even if pricing is denied; unknown rows still help totals.
			if err := a.store.UpsertResourceCosts(ctx2, rows); err != nil {
				return ScanResult{}, err
			}

			if atomic.LoadInt32(&pricingDenied) != 0 && atomic.CompareAndSwapInt32(&pricingDeniedRecorded, 0, 1) {
				failures = append(failures, ScanStepFailure{
					Phase:      PhaseCost,
					ProviderID: "pricing",
					Region:     "us-east-1",
					Error:      "Pricing API unavailable (AccessDenied); costs will be unknown",
				})
			}

			completed++
			atomic.StoreInt32(&committed, int32(completed))
			if progress != nil {
				progress(ScanProgressEvent{
					Phase:              PhaseCost,
					ProviderID:         sid,
					Region:             "all",
					Message:            "done",
					TotalSteps:         totalSteps,
					CompletedSteps:     completed,
					ResourcesSoFar:     int(atomic.LoadInt64(&nodesSoFar)),
					EdgesSoFar:         int(atomic.LoadInt64(&edgesSoFar)),
					StepResourcesAdded: len(rows),
					StepEdgesAdded:     0,
					StepTypeCounts:     typeCounts,
				})
			}
		}

		// Persist any newly fetched pricing cache entries.
		if pc != nil {
			if pending := pc.PendingRows(); len(pending) > 0 {
				if err := a.store.UpsertPricingCache(ctx2, pending); err != nil {
					return ScanResult{}, err
				}
			}
		}
	}

	summary := ScanSummary{
		Pricing: ScanPricingSummary{
			Currency: "USD",
		},
	}

	if len(serviceCounts) > 0 {
		summary.ServiceCounts = make([]ScanServiceCount, 0, len(serviceCounts))
		for service, count := range serviceCounts {
			if strings.TrimSpace(service) == "" {
				continue
			}
			summary.ServiceCounts = append(summary.ServiceCounts, ScanServiceCount{
				Service:   service,
				Resources: count,
			})
		}
		sort.Slice(summary.ServiceCounts, func(i, j int) bool {
			if summary.ServiceCounts[i].Resources != summary.ServiceCounts[j].Resources {
				return summary.ServiceCounts[i].Resources > summary.ServiceCounts[j].Resources
			}
			return summary.ServiceCounts[i].Service < summary.ServiceCounts[j].Service
		})
	}

	if len(regionCounts) > 0 {
		type regionCount struct {
			region string
			count  int
		}
		hasNonGlobal := false
		for region := range regionCounts {
			if strings.TrimSpace(region) != "" && region != "global" {
				hasNonGlobal = true
				break
			}
		}
		regionRows := make([]regionCount, 0, len(regionCounts))
		for region, count := range regionCounts {
			region = strings.TrimSpace(region)
			if region == "" {
				continue
			}
			if hasNonGlobal && region == "global" {
				continue
			}
			regionRows = append(regionRows, regionCount{
				region: region,
				count:  count,
			})
		}
		sort.Slice(regionRows, func(i, j int) bool {
			if regionRows[i].count != regionRows[j].count {
				return regionRows[i].count > regionRows[j].count
			}
			return regionRows[i].region < regionRows[j].region
		})
		if len(regionRows) > 5 {
			regionRows = regionRows[:5]
		}
		totalResources := int(atomic.LoadInt64(&nodesSoFar))
		summary.ImportantRegions = make([]ScanRegionCount, 0, len(regionRows))
		for _, rr := range regionRows {
			share := 0.0
			if totalResources > 0 {
				share = float64(rr.count) / float64(totalResources) * 100
			}
			summary.ImportantRegions = append(summary.ImportantRegions, ScanRegionCount{
				Region:    rr.region,
				Resources: rr.count,
				SharePct:  share,
			})
		}
	}

	pricingRegions := make([]string, 0, len(opts.Regions)+1)
	seenPricingRegion := map[string]struct{}{}
	for _, region := range append(append([]string{}, opts.Regions...), "global") {
		region = strings.TrimSpace(region)
		if region == "" {
			continue
		}
		if _, ok := seenPricingRegion[region]; ok {
			continue
		}
		seenPricingRegion[region] = struct{}{}
		pricingRegions = append(pricingRegions, region)
	}
	listCostAgg := a.listServiceCostAgg
	if listCostAgg == nil {
		listCostAgg = a.store.ListServiceCostAggByRegions
	}
	pricingRows, err := listCostAgg(ctx2, id.AccountID, pricingRegions)
	if err != nil {
		failures = append(failures, ScanStepFailure{
			Phase:      PhaseCost,
			ProviderID: "summary",
			Region:     "all",
			Error:      fmt.Sprintf("build pricing summary: %v", err),
		})
	} else {
		for _, row := range pricingRows {
			if _, ok := serviceCounts[row.Key]; !ok {
				continue
			}
			summary.Pricing.KnownUSD += row.KnownUSD
			summary.Pricing.UnknownCount += row.UnknownCount
		}
	}

	return ScanResult{
		Resources:    int(atomic.LoadInt64(&nodesSoFar)),
		Edges:        int(atomic.LoadInt64(&edgesSoFar)),
		AccountID:    id.AccountID,
		Partition:    id.Partition,
		StepFailures: failures,
		Summary:      summary,
	}, nil
}
