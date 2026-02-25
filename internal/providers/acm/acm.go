package acm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"awscope/internal/graph"
	"awscope/internal/providers"
	"awscope/internal/providers/registry"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdkacm "github.com/aws/aws-sdk-go-v2/service/acm"
	"github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/smithy-go"
)

func init() {
	registry.Register(New())
}

type Provider struct {
	newACM func(cfg awsSDK.Config) acmAPI
}

func New() *Provider {
	return &Provider{newACM: func(cfg awsSDK.Config) acmAPI { return sdkacm.NewFromConfig(cfg) }}
}

func (p *Provider) ID() string          { return "acm" }
func (p *Provider) DisplayName() string { return "ACM" }
func (p *Provider) Scope() providers.ScopeKind {
	return providers.ScopeRegional
}

type acmAPI interface {
	ListCertificates(ctx context.Context, params *sdkacm.ListCertificatesInput, optFns ...func(*sdkacm.Options)) (*sdkacm.ListCertificatesOutput, error)
	DescribeCertificate(ctx context.Context, params *sdkacm.DescribeCertificateInput, optFns ...func(*sdkacm.Options)) (*sdkacm.DescribeCertificateOutput, error)
}

func (p *Provider) List(ctx context.Context, cfg awsSDK.Config, req providers.ListRequest) (providers.ListResult, error) {
	if len(req.Regions) == 0 {
		return providers.ListResult{}, fmt.Errorf("acm provider requires at least one region")
	}
	if req.AccountID == "" || req.Partition == "" {
		return providers.ListResult{}, fmt.Errorf("acm provider requires account identity")
	}

	var res providers.ListResult
	for _, region := range req.Regions {
		c := cfg
		c.Region = region
		nodes, edges, err := p.listRegion(ctx, p.newACM(c), req.Partition, req.AccountID, region)
		if err != nil {
			return providers.ListResult{}, err
		}
		res.Nodes = append(res.Nodes, nodes...)
		res.Edges = append(res.Edges, edges...)
	}
	return res, nil
}

func (p *Provider) listRegion(ctx context.Context, api acmAPI, partition, accountID, region string) ([]graph.ResourceNode, []graph.RelationshipEdge, error) {
	now := time.Now().UTC()
	var nodes []graph.ResourceNode
	var edges []graph.RelationshipEdge

	allStatuses := types.CertificateStatus("").Values()
	allKeyTypes := types.KeyAlgorithm("").Values()

	var summaries []types.CertificateSummary
	var nextToken *string
	for {
		out, err := api.ListCertificates(ctx, &sdkacm.ListCertificatesInput{
			NextToken:           nextToken,
			CertificateStatuses: allStatuses,
			Includes:            &types.Filters{KeyTypes: allKeyTypes},
		})
		if err != nil {
			return nil, nil, err
		}
		summaries = append(summaries, out.CertificateSummaryList...)

		if out.NextToken == nil || strings.TrimSpace(*out.NextToken) == "" {
			break
		}
		nextToken = out.NextToken
	}

	type certResult struct {
		nodes []graph.ResourceNode
		edges []graph.RelationshipEdge
		err   error
	}
	results := make([]certResult, len(summaries))
	conc := envIntOr("AWSCOPE_ACM_DESCRIBE_CONCURRENCY", 8)
	if conc > len(summaries) {
		conc = len(summaries)
	}
	if conc < 1 {
		conc = 1
	}
	type job struct {
		idx int
		c   types.CertificateSummary
	}
	jobs := make(chan job, len(summaries))
	for i, c := range summaries {
		jobs <- job{idx: i, c: c}
	}
	close(jobs)

	var wg sync.WaitGroup
	worker := func() {
		defer wg.Done()
		for j := range jobs {
			n, stubs, es := normalizeCertificateSummary(partition, accountID, region, j.c, nil, now)

			certArn := strings.TrimSpace(awsToString(j.c.CertificateArn))
			if certArn != "" {
				detail, err := api.DescribeCertificate(ctx, &sdkacm.DescribeCertificateInput{CertificateArn: awsSDK.String(certArn)})
				if err == nil {
					n, stubs, es = normalizeCertificateSummary(partition, accountID, region, j.c, detail, now)
				} else if !isAPIErrorCode(err, "AccessDeniedException") && !isAPIErrorCode(err, "AccessDenied") && !isAPIErrorCode(err, "ResourceNotFoundException") {
					results[j.idx] = certResult{err: err}
					continue
				}
			}
			ns := make([]graph.ResourceNode, 0, 1+len(stubs))
			ns = append(ns, n)
			ns = append(ns, stubs...)
			results[j.idx] = certResult{
				nodes: ns,
				edges: es,
			}
		}
	}
	wg.Add(conc)
	for i := 0; i < conc; i++ {
		go worker()
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		nodes = append(nodes, r.nodes...)
		edges = append(edges, r.edges...)
	}

	return nodes, edges, nil
}

func normalizeCertificateSummary(partition, accountID, region string, c types.CertificateSummary, d *sdkacm.DescribeCertificateOutput, now time.Time) (graph.ResourceNode, []graph.ResourceNode, []graph.RelationshipEdge) {
	arn := strings.TrimSpace(awsToString(c.CertificateArn))
	domain := strings.TrimSpace(awsToString(c.DomainName))
	primary := firstNonEmpty(arn, domain)
	key := graph.EncodeResourceKey(partition, accountID, region, "acm:certificate", primary)
	attrs := map[string]any{
		"status":           strings.TrimSpace(string(c.Status)),
		"domain":           domain,
		"inUse":            awsToBool(c.InUse),
		"type":             strings.TrimSpace(string(c.Type)),
		"keyAlgorithm":     strings.TrimSpace(string(c.KeyAlgorithm)),
		"managedBy":        strings.TrimSpace(string(c.ManagedBy)),
		"exportOption":     strings.TrimSpace(string(c.ExportOption)),
		"hasAdditionalSAN": awsToBool(c.HasAdditionalSubjectAlternativeNames),
	}
	if c.CreatedAt != nil {
		attrs["created_at"] = c.CreatedAt.UTC().Format("2006-01-02 15:04")
	}
	if c.NotAfter != nil {
		attrs["notAfter"] = c.NotAfter.UTC().Format("2006-01-02 15:04")
	}

	var inUseBy []string
	if d != nil && d.Certificate != nil {
		cd := d.Certificate
		if cd.CreatedAt != nil {
			attrs["created_at"] = cd.CreatedAt.UTC().Format("2006-01-02 15:04")
		}
		if cd.NotAfter != nil {
			attrs["notAfter"] = cd.NotAfter.UTC().Format("2006-01-02 15:04")
		}
		if cd.NotBefore != nil {
			attrs["notBefore"] = cd.NotBefore.UTC().Format("2006-01-02 15:04")
		}
		if strings.TrimSpace(string(cd.Status)) != "" {
			attrs["status"] = strings.TrimSpace(string(cd.Status))
		}
		if strings.TrimSpace(string(cd.Type)) != "" {
			attrs["type"] = strings.TrimSpace(string(cd.Type))
		}
		inUseBy = append(inUseBy, cd.InUseBy...)
		attrs["inUseByCount"] = len(inUseBy)
	}

	raw, _ := json.Marshal(map[string]any{"summary": c, "detail": d})
	node := graph.ResourceNode{
		Key:         key,
		DisplayName: firstNonEmpty(domain, shortArn(arn), arn),
		Service:     "acm",
		Type:        "acm:certificate",
		Arn:         arn,
		PrimaryID:   primary,
		Tags:        map[string]string{},
		Attributes:  attrs,
		Raw:         raw,
		CollectedAt: now,
		Source:      "acm",
	}

	var stubs []graph.ResourceNode
	var edges []graph.RelationshipEdge
	for _, target := range inUseBy {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		toKey, svc, typ, disp, ok := acmInUseArnToNode(partition, accountID, region, target)
		if !ok {
			continue
		}
		stubs = append(stubs, stubNode(toKey, svc, typ, disp, now, "acm"))
		edges = append(edges, graph.RelationshipEdge{From: key, To: toKey, Kind: "attached-to", Meta: map[string]any{"direct": true, "source": "acm.in-use"}, CollectedAt: now})
	}

	return node, stubs, edges
}

func acmInUseArnToNode(partition, accountID, fallbackRegion, arn string) (graph.ResourceKey, string, string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(arn), ":", 6)
	if len(parts) < 6 {
		return "", "", "", "", false
	}
	svc := strings.TrimSpace(parts[2])
	region := strings.TrimSpace(parts[3])
	if region == "" {
		region = fallbackRegion
	}
	res := strings.TrimSpace(parts[5])

	switch svc {
	case "elasticloadbalancing":
		if strings.HasPrefix(res, "loadbalancer/") {
			return graph.EncodeResourceKey(partition, accountID, region, "elbv2:load-balancer", arn), "elbv2", "elbv2:load-balancer", shortArn(arn), true
		}
		if strings.HasPrefix(res, "listener/") {
			return graph.EncodeResourceKey(partition, accountID, region, "elbv2:listener", arn), "elbv2", "elbv2:listener", shortArn(arn), true
		}
	case "cloudfront":
		return graph.EncodeResourceKey(partition, accountID, "global", "cloudfront:distribution", arn), "cloudfront", "cloudfront:distribution", shortArn(arn), true
	case "apigateway":
		return graph.EncodeResourceKey(partition, accountID, region, "apigateway:resource", arn), "apigateway", "apigateway:resource", shortArn(arn), true
	}
	return "", "", "", "", false
}

func stubNode(key graph.ResourceKey, service, typ, display string, now time.Time, source string) graph.ResourceNode {
	_, _, _, _, primaryID, err := graph.ParseResourceKey(key)
	if err != nil {
		primaryID = ""
	}
	return graph.ResourceNode{
		Key:         key,
		DisplayName: display,
		Service:     service,
		Type:        typ,
		PrimaryID:   primaryID,
		Tags:        map[string]string{},
		Attributes:  map[string]any{"stub": true},
		Raw:         []byte(`{}`),
		CollectedAt: now,
		Source:      source,
	}
}

func awsToString[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func awsToBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}

func shortArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, "/"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func isAPIErrorCode(err error, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ae.ErrorCode()), strings.TrimSpace(code))
}

func envIntOr(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
