package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"awscope/internal/store"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkpricing "github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/pricing/types"
	"golang.org/x/sync/singleflight"
)

type Query struct {
	Partition   string
	ServiceCode string
	PriceKind   string

	AWSRegion string // the region we're pricing for (not the pricing API region)
	Location  string // pricing "location" string

	Filters map[string]string // Pricing API term match filters (excluding location)
}

type Price struct {
	USD  *float64
	Unit string
	Raw  string

	CacheKey    string
	RetrievedAt time.Time
}

type Client struct {
	st  *store.Store
	api *sdkpricing.Client

	staleAfter time.Duration
	// When we cache "not found" (USD == nil), keep the negative cache short so fixes
	// to parsing/filtering can self-heal without waiting for the full TTL.
	negativeStaleAfter time.Duration

	mu    sync.Mutex
	mem   map[string]store.PricingCacheRow // cache_key -> row
	dirty map[string]bool                  // cache_key -> should upsert
	sf    singleflight.Group
}

type Options struct {
	StaleAfter time.Duration
}

func NewClient(st *store.Store, cfg awsSDK.Config, opts Options) *Client {
	c := cfg
	// Pricing API is effectively global for aws partition; SDK expects a region.
	c.Region = "us-east-1"

	stale := opts.StaleAfter
	if stale <= 0 {
		stale = 7 * 24 * time.Hour
	}

	return &Client{
		st:         st,
		api:        sdkpricing.NewFromConfig(c),
		staleAfter: stale,
		negativeStaleAfter: 1 * time.Hour,
		mem:        map[string]store.PricingCacheRow{},
		dirty:      map[string]bool{},
	}
}

func (c *Client) PendingRows() []store.PricingCacheRow {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]store.PricingCacheRow, 0, len(c.dirty))
	for k := range c.dirty {
		if r, ok := c.mem[k]; ok {
			out = append(out, r)
		}
	}
	return out
}

func (c *Client) Lookup(ctx context.Context, q Query) (Price, error) {
	q.Partition = strings.TrimSpace(q.Partition)
	if q.Partition == "" {
		q.Partition = "aws"
	}
	q.ServiceCode = strings.TrimSpace(q.ServiceCode)
	q.PriceKind = strings.TrimSpace(q.PriceKind)
	q.AWSRegion = strings.TrimSpace(q.AWSRegion)
	q.Location = strings.TrimSpace(q.Location)
	if q.ServiceCode == "" || q.PriceKind == "" || q.Location == "" {
		return Price{}, fmt.Errorf("invalid pricing query (missing service_code/price_kind/location)")
	}

	filtersJSON, err := canonicalFiltersJSON(q.Filters)
	if err != nil {
		return Price{}, err
	}

	ck := cacheKey(q.Partition, q.ServiceCode, q.PriceKind, q.AWSRegion, q.Location, filtersJSON)

	// Fast path: in-memory.
	if r, ok := c.getFreshFromMem(ck); ok {
		return rowToPrice(r), nil
	}

	// Use singleflight to coalesce concurrent misses.
	v, err, _ := c.sf.Do(ck, func() (any, error) {
		// Re-check after coalescing.
		if r, ok := c.getFreshFromMem(ck); ok {
			return rowToPrice(r), nil
		}

		// DB cache.
		if c.st != nil {
			if rr, ok, err := c.st.GetPricingCache(ctx, ck); err == nil && ok {
				if !c.isStaleRow(rr) {
					c.mu.Lock()
					c.mem[ck] = rr
					c.mu.Unlock()
					return rowToPrice(rr), nil
				}
			} else if err != nil {
				// If DB read fails, fall back to API fetch.
			}
		}

		// Fetch from API.
		price, row, err := c.fetchFromAPI(ctx, ck, q, filtersJSON)
		if err != nil {
			return Price{}, err
		}
		c.mu.Lock()
		c.mem[ck] = row
		c.dirty[ck] = true
		c.mu.Unlock()
		return price, nil
	})
	if err != nil {
		return Price{}, err
	}
	return v.(Price), nil
}

func (c *Client) getFreshFromMem(cacheKey string) (store.PricingCacheRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.mem[cacheKey]
	if !ok {
		return store.PricingCacheRow{}, false
	}
	if c.isStaleRow(r) {
		return store.PricingCacheRow{}, false
	}
	return r, true
}

func (c *Client) isStale(t time.Time) bool {
	if t.IsZero() {
		return true
	}
	return time.Since(t) > c.staleAfter
}

func (c *Client) isStaleRow(r store.PricingCacheRow) bool {
	if r.RetrievedAt.IsZero() {
		return true
	}
	// Treat negative cache entries as stale so changes to filters/parsing take effect immediately.
	// This may increase Pricing API calls for truly-unknown items; we can reintroduce a bounded
	// negative TTL once estimators stabilize.
	if r.USD == nil {
		return true
	}
	ttl := c.staleAfter
	if r.USD == nil && c.negativeStaleAfter > 0 {
		ttl = c.negativeStaleAfter
	}
	return time.Since(r.RetrievedAt) > ttl
}

func rowToPrice(r store.PricingCacheRow) Price {
	return Price{
		USD:         r.USD,
		Unit:        r.Unit,
		Raw:         r.RawJSON,
		CacheKey:    r.CacheKey,
		RetrievedAt: r.RetrievedAt,
	}
}

func (c *Client) fetchFromAPI(ctx context.Context, cacheKey string, q Query, filtersJSON string) (Price, store.PricingCacheRow, error) {
	// Pricing API term match filters.
	var filters []types.Filter
	filters = append(filters, types.Filter{
		Type:  types.FilterTypeTermMatch,
		Field: awsSDK.String("location"),
		Value: awsSDK.String(q.Location),
	})

	// Canonicalize for stable requests.
	keys := make([]string, 0, len(q.Filters))
	for k := range q.Filters {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		v := strings.TrimSpace(q.Filters[k])
		if v == "" {
			continue
		}
		filters = append(filters, types.Filter{
			Type:  types.FilterTypeTermMatch,
			Field: awsSDK.String(k),
			Value: awsSDK.String(v),
		})
	}

	// Some services (notably load balancing and Fargate) have multiple relevant price dimensions
	// or many similar products. In those cases we fetch a larger page and choose the best match
	// based on price kind.
	maxResults := int32(1)
	if wantsSearch(q.PriceKind) {
		maxResults = 100
	}

	raw := "{}"
	usd, unit := (*float64)(nil), "-"

	var token *string
	for {
		out, err := c.api.GetProducts(ctx, &sdkpricing.GetProductsInput{
			ServiceCode: awsSDK.String(q.ServiceCode),
			Filters:     filters,
			MaxResults:  awsSDK.Int32(maxResults),
			NextToken:   token,
		})
		if err != nil {
			return Price{}, store.PricingCacheRow{}, err
		}

		// Try each returned product until we find a usable price for this kind.
		for _, pl := range out.PriceList {
			u, un := parseUSDAndUnitForKind(q.PriceKind, pl)
			if u != nil {
				usd, unit = u, un
				raw = pl
				break
			}
			// Keep one raw example for debugging even if it's not a match.
			if raw == "{}" && strings.TrimSpace(pl) != "" {
				raw = pl
			}
		}

		if usd != nil || !wantsSearch(q.PriceKind) {
			break
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}

	now := time.Now().UTC()

	row := store.PricingCacheRow{
		CacheKey:    cacheKey,
		Partition:   q.Partition,
		ServiceCode: q.ServiceCode,
		PriceKind:   q.PriceKind,
		AWSRegion:   q.AWSRegion,
		Location:    q.Location,
		FiltersJSON: filtersJSON,
		Unit:        unit,
		USD:         usd,
		RawJSON:     raw,
		RetrievedAt: now,
	}

	return rowToPrice(row), row, nil
}

func wantsSearch(priceKind string) bool {
	priceKind = strings.ToLower(strings.TrimSpace(priceKind))
	switch {
	case strings.HasPrefix(priceKind, "fargate_"):
		return true
	case strings.HasPrefix(priceKind, "elb_"):
		return true
	}
	return false
}

func parseUSDAndUnitForKind(priceKind, raw string) (*float64, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil, "-"
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, "-"
	}

	terms, ok := obj["terms"].(map[string]any)
	if !ok {
		return nil, "-"
	}
	onDemand, ok := terms["OnDemand"].(map[string]any)
	if !ok || len(onDemand) == 0 {
		return nil, "-"
	}
	// First term.
	var term any
	for _, v := range onDemand {
		term = v
		break
	}
	termObj, ok := term.(map[string]any)
	if !ok {
		return nil, "-"
	}
	pds, ok := termObj["priceDimensions"].(map[string]any)
	if !ok || len(pds) == 0 {
		return nil, "-"
	}

	// Choose the best price dimension for this kind.
	want := strings.ToLower(strings.TrimSpace(priceKind))
	bestScore := -1
	var best map[string]any
	for _, v := range pds {
		pdObj, ok := v.(map[string]any)
		if !ok {
			continue
		}
		score := scorePriceDimension(want, pdObj)
		if score > bestScore {
			bestScore = score
			best = pdObj
		}
	}
	if best == nil || bestScore < minScoreForKind(want) {
		return nil, "-"
	}

	unit, _ := best["unit"].(string)
	pp, ok := best["pricePerUnit"].(map[string]any)
	if !ok {
		return nil, unitOrDash(unit)
	}
	usdStr, _ := pp["USD"].(string)
	usdStr = strings.TrimSpace(usdStr)
	if usdStr == "" {
		return nil, unitOrDash(unit)
	}
	f, err := strconv.ParseFloat(usdStr, 64)
	if err != nil {
		return nil, unitOrDash(unit)
	}
	return &f, unitOrDash(unit)
}

func minScoreForKind(priceKind string) int {
	// In search mode we may see many irrelevant products/dimensions. Returning nil for
	// weak matches prevents poisoning the cache and avoids wildly wrong estimates.
	switch {
	case strings.HasPrefix(priceKind, "fargate_"):
		return 60
	case strings.HasPrefix(priceKind, "elb_"):
		return 40
	}
	return 1
}

func scorePriceDimension(priceKind string, pd map[string]any) int {
	// Higher score = better match.
	unit, _ := pd["unit"].(string)
	unit = strings.ToLower(strings.TrimSpace(unit))
	desc, _ := pd["description"].(string)
	desc = strings.ToLower(strings.TrimSpace(desc))

	score := 0
	// Strong negative signals first.
	if strings.HasPrefix(priceKind, "elb_") {
		// For ELB "hour" kinds we want LB-hours, not data processing or other GB-based dimensions.
		if strings.Contains(unit, "gb") {
			score -= 200
		}
		if strings.Contains(desc, "data processing") || strings.Contains(desc, "dataprocessing") || strings.Contains(desc, "per gb") {
			score -= 200
		}
	}

	switch {
	case strings.Contains(priceKind, "gb_month"):
		if strings.Contains(unit, "gb") && (strings.Contains(unit, "mo") || strings.Contains(unit, "month")) {
			score += 50
		}
	case strings.Contains(priceKind, "hourly") || strings.Contains(priceKind, "lb_hour") || strings.Contains(priceKind, "hour"):
		if strings.Contains(unit, "hr") || strings.Contains(unit, "hour") {
			score += 30
		}
	}

	switch {
	case strings.Contains(priceKind, "fargate_vcpu"):
		if strings.Contains(unit, "vcpu") || strings.Contains(desc, "vcpu") {
			score += 80
		} else {
			// Avoid matching "ECS managed instances" style hourly SKUs.
			score -= 200
		}
		// vCPU pricing should not match GB-hour dimensions.
		if strings.Contains(unit, "gb") {
			score -= 200
		}
	case strings.Contains(priceKind, "fargate_gb"):
		if strings.Contains(unit, "gb") && strings.Contains(unit, "hour") {
			score += 80
		}
		if strings.Contains(desc, "gb") && strings.Contains(desc, "hour") {
			score += 60
		}
		// In Pricing API, Fargate memory is commonly expressed as "hours" with a "Memory" description,
		// but the numeric value is per GB-hour. Accept that shape.
		if strings.Contains(desc, "memory") {
			score += 80
		}
		if !(strings.Contains(unit, "gb") || strings.Contains(desc, "gb") || strings.Contains(desc, "memory")) {
			score -= 200
		}
	case strings.Contains(priceKind, "elb_alb"):
		// Prefer LB-hour, not LCU-hour.
		if strings.Contains(desc, "lcu") {
			score -= 100
		}
		if strings.Contains(desc, "dataprocessing") || strings.Contains(desc, "data processing") {
			score -= 200
		}
		if strings.Contains(desc, "loadbalancerusage") || strings.Contains(desc, "load balancerusage") {
			score += 40
		}
		if strings.Contains(desc, "load balancer") || strings.Contains(desc, "loadbalancer") {
			score += 60
		}
	case strings.Contains(priceKind, "elb_nlb"):
		if strings.Contains(desc, "lcu") {
			score -= 100
		}
		if strings.Contains(desc, "dataprocessing") || strings.Contains(desc, "data processing") {
			score -= 200
		}
		if strings.Contains(desc, "loadbalancerusage") || strings.Contains(desc, "load balancerusage") {
			score += 40
		}
		if strings.Contains(desc, "load balancer") || strings.Contains(desc, "loadbalancer") {
			score += 60
		}
	case strings.Contains(priceKind, "rds_ondemand_hourly"):
		if strings.Contains(unit, "hr") || strings.Contains(unit, "hour") {
			score += 40
		}
	case strings.Contains(priceKind, "aurora_acu"):
		if strings.Contains(unit, "acu") {
			score += 80
		}
		if strings.Contains(desc, "capacity unit") || strings.Contains(desc, "acu") {
			score += 60
		}
	case strings.Contains(priceKind, "ebs_storage"):
		if strings.Contains(unit, "gb") && strings.Contains(unit, "mo") {
			score += 60
		}
	}

	// Prefer non-zero prices when multiple dimensions exist (like ELB LCU + LB-hour).
	if pp, ok := pd["pricePerUnit"].(map[string]any); ok {
		if usdStr, _ := pp["USD"].(string); strings.TrimSpace(usdStr) != "" {
			if f, err := strconv.ParseFloat(strings.TrimSpace(usdStr), 64); err == nil && f > 0 {
				score += 5
			}
		}
	}
	return score
}

func unitOrDash(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return "-"
	}
	return u
}

func sortStrings(xs []string) {
	// tiny local helper to avoid importing sort in multiple files
	for i := 0; i < len(xs); i++ {
		for j := i + 1; j < len(xs); j++ {
			if xs[j] < xs[i] {
				xs[i], xs[j] = xs[j], xs[i]
			}
		}
	}
}
