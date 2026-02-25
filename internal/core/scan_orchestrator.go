package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

	stalePolicy := opts.StalePolicy
	if stalePolicy == "" {
		stalePolicy = StalePolicyHide
	}
	if stalePolicy != StalePolicyHide && stalePolicy != StalePolicyOff {
		return ScanResult{}, fmt.Errorf("invalid stale policy %q", stalePolicy)
	}

	scanID := newScanID()
	scopeJSONBytes, _ := json.Marshal(map[string]any{
		"profile":        profileName,
		"account_id":     id.AccountID,
		"partition":      id.Partition,
		"regions":        opts.Regions,
		"provider_ids":   opts.ProviderIDs,
		"stale_policy":   stalePolicy,
		"started_at_rfc": now.Format(time.RFC3339Nano),
	})
	_ = a.store.StartScanRun(ctx, scanID, profileName, string(scopeJSONBytes), now)
	scanSucceeded := false
	defer func() {
		status := "failed"
		if scanSucceeded {
			status = "success"
		}
		_ = a.store.FinishScanRun(context.Background(), scanID, status, time.Now().UTC())
	}()

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
	successfulScopes := map[string]map[string]struct{}{}
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

	scanStartedAt := time.Now()
	phaseDurations := map[ScanProgressPhase]time.Duration{}
	var slowSteps []ScanSlowStep
	addSlowStep := func(phase ScanProgressPhase, providerID, region string, d time.Duration) {
		if d <= 0 {
			return
		}
		slowSteps = append(slowSteps, ScanSlowStep{
			Phase:      phase,
			ProviderID: providerID,
			Region:     region,
			Duration:   d,
		})
	}

	maxConc := opts.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 16
	}
	resolverConc := opts.ResolverConcurrency
	if resolverConc <= 0 {
		resolverConc = 8
	}
	auditRegionConc := opts.AuditRegionConcurrency
	if auditRegionConc <= 0 {
		auditRegionConc = 10
	}
	auditSourceConc := opts.AuditSourceConcurrency
	if auditSourceConc <= 0 {
		auditSourceConc = 3
	}
	auditLookupInterval := opts.AuditLookupInterval
	if auditLookupInterval < 0 {
		auditLookupInterval = 0
	}
	elbv2TargetHealthConc := opts.ELBv2TargetHealthConcurrency
	if elbv2TargetHealthConc <= 0 {
		elbv2TargetHealthConc = 30
	}
	costConc := opts.CostConcurrency
	if costConc <= 0 {
		costConc = 16
	}
	targetDuration := opts.TargetDuration
	if targetDuration <= 0 {
		targetDuration = 60 * time.Second
	}

	type scanTask struct {
		phase      ScanProgressPhase
		providerID string
		scope      providers.ScopeKind
		stepRegion string
		startMsg   string
		reqRegions []string
		run        func(ctx context.Context) (providers.ListResult, error)
	}

	type scanTaskResult struct {
		task    scanTask
		res     providers.ListResult
		stepErr string
		elapsed time.Duration
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
				addSlowStep(r.task.phase, r.task.providerID, r.task.stepRegion, r.elapsed)
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

			if err := a.store.UpsertResourcesWithScan(ctx2, r.res.Nodes, scanID); err != nil {
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

			scopeRegions := successfulScopeRegions(r.task.scope, r.task.stepRegion, r.task.reqRegions)
			if len(scopeRegions) > 0 {
				byRegion, ok := successfulScopes[r.task.providerID]
				if !ok {
					byRegion = map[string]struct{}{}
					successfulScopes[r.task.providerID] = byRegion
				}
				for _, region := range scopeRegions {
					byRegion[region] = struct{}{}
				}
			}

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
				for rr, arns := range collectInstanceTargetGroupsByRegion(r.res.Nodes) {
					tgsByRegion[rr] = append(tgsByRegion[rr], arns...)
				}
			}

			typeCounts, sampleLabel, sampleTotal, sampleItems := stepSummary(r.task.providerID, r.res.Nodes, r.res.Edges)
			addSlowStep(r.task.phase, r.task.providerID, r.task.stepRegion, r.elapsed)
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

	providerStageStartedAt := time.Now()

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
				scope:      providers.ScopeGlobal,
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
				scope:      providers.ScopeAccount,
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
					scope:      providers.ScopeRegional,
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
				started := time.Now()
				res, err := t.run(gctx)
				elapsed := time.Since(started)
				if err != nil {
					if isSkippableStepError(err) {
						select {
						case resultsCh <- scanTaskResult{task: t, stepErr: err.Error(), elapsed: elapsed}:
						case <-gctx.Done():
							return gctx.Err()
						}
						continue
					}
					cancel()
					return fmt.Errorf("%s/%s: %w", t.providerID, t.stepRegion, err)
				}
				select {
				case resultsCh <- scanTaskResult{task: t, res: res, elapsed: elapsed}:
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
	phaseDurations[PhaseProvider] = time.Since(providerStageStartedAt)

	if stalePolicy == StalePolicyHide {
		scopes := make([]store.ScanScope, 0, len(successfulScopes))
		for service, regionsSet := range successfulScopes {
			if strings.TrimSpace(service) == "" || len(regionsSet) == 0 {
				continue
			}
			regions := make([]string, 0, len(regionsSet))
			for region := range regionsSet {
				region = strings.TrimSpace(region)
				if region == "" {
					continue
				}
				regions = append(regions, region)
			}
			if len(regions) == 0 {
				continue
			}
			sort.Strings(regions)
			scopes = append(scopes, store.ScanScope{
				Service: service,
				Regions: regions,
			})
		}
		if len(scopes) > 0 {
			if _, err := a.store.MarkResourcesStaleNotSeenInScopes(ctx2, id.AccountID, scanID, scopes, time.Now().UTC()); err != nil {
				return ScanResult{}, err
			}
		}
	}

	// Resolver: instance -> target group membership (full mapping v1 slice).
	if needsResolver {
		resolverStageStartedAt := time.Now()
		resolveTargetGroups := a.resolveTargetGroups
		if resolveTargetGroups == nil {
			resolveTargetGroups = resolveInstanceTargetGroups
		}
		type resolverResult struct {
			region  string
			tgs     int
			edges   []graph.RelationshipEdge
			err     string
			elapsed time.Duration
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
					addSlowStep(PhaseResolver, "elbv2", rr.region, rr.elapsed)
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
				addSlowStep(PhaseResolver, "elbv2", rr.region, rr.elapsed)
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
					if len(tgs) > 1 {
						seen := make(map[string]struct{}, len(tgs))
						deduped := make([]string, 0, len(tgs))
						for _, arn := range tgs {
							arn = strings.TrimSpace(arn)
							if arn == "" {
								continue
							}
							if _, ok := seen[arn]; ok {
								continue
							}
							seen[arn] = struct{}{}
							deduped = append(deduped, arn)
						}
						tgs = deduped
					}
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
					started := time.Now()
					edges, err := resolveTargetGroups(rctx, cfg, id.Partition, id.AccountID, refs, elbv2TargetHealthConc)
					elapsed := time.Since(started)
					if err != nil {
						if isSkippableStepError(err) {
							// Best-effort skip resolver errors too (rare; depends on permissions).
							select {
							case resCh <- resolverResult{region: region, tgs: len(tgs), err: err.Error(), elapsed: elapsed}:
							case <-rctx.Done():
								return rctx.Err()
							}
							continue
						}
						cancel()
						return err
					}
					select {
					case resCh <- resolverResult{region: region, tgs: len(tgs), edges: edges, elapsed: elapsed}:
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
		phaseDurations[PhaseResolver] = time.Since(resolverStageStartedAt)
	}

	// CloudTrail audit indexing: create/delete management events for high-impact services.
	// Best effort for access-denied regions; hard-fail on other errors.
	if needsAudit {
		auditStageStartedAt := time.Now()
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
			LookupInterval:     auditLookupInterval,
			SourceConcurrency:  auditSourceConc,
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
			elapsed   time.Duration
		}

		results := make(chan auditResult, len(auditRegions))
		auditConc := auditRegionConc
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
							Message:        fmt.Sprintf("indexing create/delete management events (%dd)", windowDays),
							TotalSteps:     totalSteps,
							CompletedSteps: int(atomic.LoadInt32(&committed)),
							ResourcesSoFar: int(atomic.LoadInt64(&nodesSoFar)),
							EdgesSoFar:     int(atomic.LoadInt64(&edgesSoFar)),
						})
					}

					started := time.Now()
					regionRes, err := indexer.IndexRegion(actx, region)
					elapsed := time.Since(started)
					if err != nil {
						if audittrail.IsAccessDenied(err) || audittrail.IsThrottled(err) || isEndpointUnavailable(err) {
							select {
							case results <- auditResult{region: region, stepErr: err.Error(), elapsed: elapsed}:
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
						elapsed:   elapsed,
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
				addSlowStep(PhaseAudit, "cloudtrail", rr.region, rr.elapsed)
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
			addSlowStep(PhaseAudit, "cloudtrail", rr.region, rr.elapsed)
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
		phaseDurations[PhaseAudit] = time.Since(auditStageStartedAt)
	}

	// Cost indexing: best-effort estimated monthly cost per resource.
	// This stage never fails the scan due to Pricing API errors; it records warnings in StepFailures.
	{
		costStageStartedAt := time.Now()
		// Include "global" for providers that emit global resources (e.g. IAM).
		regionsForCost := append([]string{}, opts.Regions...)
		regionsForCost = append(regionsForCost, "global")

		pc := pricing.NewClient(a.store, cfg, pricing.Options{})

		// If Pricing is denied once, disable further pricing calls and only write unknown rows.
		var pricingDenied int32
		var pricingDeniedRecorded int32

		for _, sid := range opts.ProviderIDs {
			sid := sid
			serviceStarted := time.Now()
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
			addSlowStep(PhaseCost, sid, "all", time.Since(serviceStarted))
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
		phaseDurations[PhaseCost] = time.Since(costStageStartedAt)
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

	totalDuration := time.Since(scanStartedAt)
	sort.Slice(slowSteps, func(i, j int) bool {
		if slowSteps[i].Duration != slowSteps[j].Duration {
			return slowSteps[i].Duration > slowSteps[j].Duration
		}
		if slowSteps[i].Phase != slowSteps[j].Phase {
			return slowSteps[i].Phase < slowSteps[j].Phase
		}
		if slowSteps[i].ProviderID != slowSteps[j].ProviderID {
			return slowSteps[i].ProviderID < slowSteps[j].ProviderID
		}
		return slowSteps[i].Region < slowSteps[j].Region
	})
	if len(slowSteps) > 10 {
		slowSteps = slowSteps[:10]
	}
	perf := ScanPerformanceSummary{
		TotalDuration:  totalDuration,
		TargetDuration: targetDuration,
		TargetMet:      totalDuration <= targetDuration,
		PhaseDurations: phaseDurations,
		SlowSteps:      slowSteps,
	}

	scanSucceeded = true
	return ScanResult{
		Resources:    int(atomic.LoadInt64(&nodesSoFar)),
		Edges:        int(atomic.LoadInt64(&edgesSoFar)),
		AccountID:    id.AccountID,
		Partition:    id.Partition,
		ScanID:       scanID,
		StepFailures: failures,
		Summary:      summary,
		Performance:  perf,
	}, nil
}

func successfulScopeRegions(scope providers.ScopeKind, stepRegion string, reqRegions []string) []string {
	seen := map[string]struct{}{}
	add := func(region string) {
		region = strings.TrimSpace(region)
		if region == "" {
			return
		}
		seen[region] = struct{}{}
	}

	switch scope {
	case providers.ScopeGlobal:
		add("global")
	case providers.ScopeAccount:
		for _, region := range reqRegions {
			add(region)
		}
		add("global")
	default:
		add(stepRegion)
	}

	out := make([]string, 0, len(seen))
	for region := range seen {
		out = append(out, region)
	}
	return out
}

func newScanID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("scan-%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("scan-%d-%s", time.Now().UTC().UnixNano(), hex.EncodeToString(b[:]))
}
