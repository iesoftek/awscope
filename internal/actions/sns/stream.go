package sns

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"awscope/internal/actions"
	"awscope/internal/actions/registry"
	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksns "github.com/aws/aws-sdk-go-v2/service/sns"
	sdksqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

func init() {
	registry.Register(Stream{})
}

var (
	newSNSActionClient = func(cfg awsSDK.Config) snsAPI { return sdksns.NewFromConfig(cfg) }
	newSQSActionClient = func(cfg awsSDK.Config) sqsAPI { return sdksqs.NewFromConfig(cfg) }
)

type Stream struct{}

func (Stream) ID() string    { return "sns.stream" }
func (Stream) Title() string { return "Stream messages (ephemeral SQS subscriber)" }
func (Stream) PreferEmbeddedTUI() bool {
	return true
}
func (Stream) Description() string {
	return "Create a temporary SQS subscription for this topic, stream messages, then tear it down"
}
func (Stream) Risk() actions.RiskLevel { return actions.RiskMedium }

func (Stream) Applicable(node graph.ResourceNode) bool {
	if node.Service != "sns" || node.Type != "sns:topic" {
		return false
	}
	arn := strings.TrimSpace(node.Arn)
	if arn == "" {
		arn = strings.TrimSpace(node.PrimaryID)
	}
	return strings.HasPrefix(arn, "arn:")
}

func (a Stream) Execute(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (actions.Result, error) {
	return a.ExecuteTerminal(ctx, execCtx, node)
}

func (Stream) ExecuteTerminal(ctx context.Context, execCtx actions.ExecContext, node graph.ResourceNode) (res actions.Result, err error) {
	if err := requireRegional(execCtx.Region); err != nil {
		return actions.Result{}, err
	}

	in := nonNilReader(execCtx.Stdin, os.Stdin)
	out := nonNilWriter(execCtx.Stdout, os.Stdout)
	errOut := nonNilWriter(execCtx.Stderr, os.Stderr)
	prompt := newPrompter(in, out)

	snsCli := newSNSActionClient(execCtx.AWSConfig)
	sqsCli := newSQSActionClient(execCtx.AWSConfig)

	topic, err := resolveTopic(ctx, snsCli, node)
	if err != nil {
		return actions.Result{}, err
	}

	fmt.Fprintf(out, "topic: %s (%s) fifo=%t\n", topic.name, topic.arn, topic.fifo)
	ok, err := prompt.confirmStep(2, 7, "continue with ephemeral SQS stream setup")
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user at discovery confirmation")
	}

	state := sessionState{topicARN: topic.arn}
	defer func() {
		if !state.needsTeardown() {
			return
		}
		tdCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		autoApprove := execCtx.AutoApproveTeardownOnCancel && ctx.Err() != nil
		sum, tdErr := teardownSession(tdCtx, prompt, sqsCli, snsCli, execCtx, &state, out, errOut, autoApprove)
		if tdErr != nil {
			if err == nil {
				err = tdErr
			} else {
				fmt.Fprintf(errOut, "teardown error: %v\n", tdErr)
			}
		}
		if res.Data == nil {
			res.Data = map[string]any{}
		}
		res.Data["teardown"] = sum.toMap()
	}()

	queueName := streamQueueName(topic.name, topic.arn, topic.fifo)
	state.queueName = queueName
	queueURL, exists, err := getQueueURLByName(ctx, sqsCli, queueName)
	if err != nil {
		return actions.Result{}, err
	}
	if !exists {
		ok, err := prompt.confirmStep(3, 7, fmt.Sprintf("create stream queue %s", queueName))
		if err != nil {
			return actions.Result{}, err
		}
		if !ok {
			return actions.Result{}, fmt.Errorf("aborted by user before queue creation")
		}
		queueURL, err = createStreamQueue(ctx, sqsCli, queueName, topic)
		if err != nil {
			return actions.Result{}, fmt.Errorf("create stream queue: %w", err)
		}
		state.queueCreated = true
		state.queueDeleteOnExit = true
	} else {
		owned, ownErr := isOwnedActionQueue(ctx, sqsCli, queueURL, map[string]string{
			"awscope:stream_for_topic": topic.arn,
		})
		if ownErr != nil {
			fmt.Fprintf(errOut, "warning: could not inspect queue ownership tags for %s: %v\n", queueName, ownErr)
		}
		state.queueDeleteOnExit = owned
	}
	state.queueURL = queueURL

	queueARN, policyDoc, err := getQueueARNAndPolicy(ctx, sqsCli, queueURL)
	if err != nil {
		return actions.Result{}, err
	}
	state.queueARN = queueARN

	ok, err = prompt.confirmStep(4, 7, "apply/patch queue policy for SNS publish")
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user before policy update")
	}
	patched, err := ensureQueuePolicyForTopic(ctx, sqsCli, queueURL, queueARN, policyDoc, topic.arn)
	if err != nil {
		return actions.Result{}, err
	}
	state.policyPatched = patched

	ok, err = prompt.confirmStep(5, 7, "create topic subscription to stream queue if missing")
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user before subscription setup")
	}
	subARN, created, err := ensureTopicSubscription(ctx, snsCli, topic.arn, queueARN)
	if err != nil {
		return actions.Result{}, err
	}
	if created && strings.HasPrefix(subARN, "arn:") {
		state.createdSubscriptionARNs = append(state.createdSubscriptionARNs, subARN)
	}

	ok, err = prompt.confirmStep(6, 7, fmt.Sprintf("start streaming from %s", queueName))
	if err != nil {
		return actions.Result{}, err
	}
	if !ok {
		return actions.Result{}, fmt.Errorf("aborted by user before starting stream")
	}

	fmt.Fprintf(out, "streaming from %s (%s). Press Ctrl+C to stop.\n", queueName, queueURL)
	streamCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	stats, err := streamTopicMessages(streamCtx, sqsCli, queueURL, topic.arn, out, errOut)
	if err != nil {
		return actions.Result{}, err
	}

	res = actions.Result{Status: "SUCCEEDED", Data: map[string]any{
		"mode":                  "sns-ephemeral-stream",
		"topic_arn":             topic.arn,
		"topic_name":            topic.name,
		"queue_name":            queueName,
		"queue_url":             queueURL,
		"queue_arn":             queueARN,
		"queue_created":         state.queueCreated,
		"queue_delete_on_exit":  state.queueDeleteOnExit,
		"subscription_created":  created,
		"created_subscriptions": len(state.createdSubscriptionARNs),
		"policy_patched":        patched,
		"messages_streamed":     stats.Streamed,
		"messages_deleted":      stats.Deleted,
		"receive_polls":         stats.Polls,
		"profile":               execCtx.Profile,
		"region":                execCtx.Region,
		"stream_exit":           "graceful",
		"capture_mode":          "live",
		"confirmation_mode":     "per-step",
	}}
	return res, nil
}

type sqsAPI interface {
	GetQueueAttributes(ctx context.Context, params *sdksqs.GetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueAttributesOutput, error)
	GetQueueUrl(ctx context.Context, params *sdksqs.GetQueueUrlInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueUrlOutput, error)
	CreateQueue(ctx context.Context, params *sdksqs.CreateQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.CreateQueueOutput, error)
	ListQueueTags(ctx context.Context, params *sdksqs.ListQueueTagsInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ListQueueTagsOutput, error)
	SetQueueAttributes(ctx context.Context, params *sdksqs.SetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.SetQueueAttributesOutput, error)
	ReceiveMessage(ctx context.Context, params *sdksqs.ReceiveMessageInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ReceiveMessageOutput, error)
	DeleteMessageBatch(ctx context.Context, params *sdksqs.DeleteMessageBatchInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteMessageBatchOutput, error)
	DeleteQueue(ctx context.Context, params *sdksqs.DeleteQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteQueueOutput, error)
}

type snsAPI interface {
	GetTopicAttributes(ctx context.Context, params *sdksns.GetTopicAttributesInput, optFns ...func(*sdksns.Options)) (*sdksns.GetTopicAttributesOutput, error)
	ListSubscriptionsByTopic(ctx context.Context, params *sdksns.ListSubscriptionsByTopicInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsByTopicOutput, error)
	Subscribe(ctx context.Context, params *sdksns.SubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.SubscribeOutput, error)
	Unsubscribe(ctx context.Context, params *sdksns.UnsubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.UnsubscribeOutput, error)
}

type topicInfo struct {
	arn  string
	name string
	fifo bool
}

func resolveTopic(ctx context.Context, snsCli snsAPI, node graph.ResourceNode) (topicInfo, error) {
	arn := strings.TrimSpace(node.Arn)
	if arn == "" {
		arn = strings.TrimSpace(node.PrimaryID)
	}
	if !strings.HasPrefix(arn, "arn:") {
		return topicInfo{}, fmt.Errorf("sns topic missing arn")
	}
	out, err := snsCli.GetTopicAttributes(ctx, &sdksns.GetTopicAttributesInput{TopicArn: &arn})
	if err != nil {
		return topicInfo{}, fmt.Errorf("get topic attributes: %w", err)
	}
	name := topicNameFromARN(arn)
	if name == "" {
		name = strings.TrimSpace(node.DisplayName)
	}
	fifo := strings.EqualFold(strings.TrimSpace(out.Attributes["FifoTopic"]), "true") || strings.HasSuffix(name, ".fifo")
	return topicInfo{arn: arn, name: name, fifo: fifo}, nil
}

func getQueueURLByName(ctx context.Context, sqsCli sqsAPI, queueName string) (string, bool, error) {
	q := strings.TrimSpace(queueName)
	if q == "" {
		return "", false, nil
	}
	out, err := sqsCli.GetQueueUrl(ctx, &sdksqs.GetQueueUrlInput{QueueName: &q})
	if err != nil {
		if isSQSErrorCode(err, "AWS.SimpleQueueService.NonExistentQueue", "QueueDoesNotExist") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get queue url for %s: %w", q, err)
	}
	url := strings.TrimSpace(awsSDK.ToString(out.QueueUrl))
	if url == "" {
		return "", false, nil
	}
	return url, true, nil
}

func createStreamQueue(ctx context.Context, sqsCli sqsAPI, queueName string, topic topicInfo) (string, error) {
	attrs := map[string]string{}
	if topic.fifo {
		attrs[string(sqstypes.QueueAttributeNameFifoQueue)] = "true"
		attrs[string(sqstypes.QueueAttributeNameContentBasedDeduplication)] = "true"
	}
	in := &sdksqs.CreateQueueInput{
		QueueName:  awsSDK.String(queueName),
		Attributes: attrs,
		Tags: map[string]string{
			"awscope:managed_by":       "awscope",
			"awscope:stream_for_topic": topic.arn,
		},
	}
	out, err := sqsCli.CreateQueue(ctx, in)
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(awsSDK.ToString(out.QueueUrl))
	if url == "" {
		resolved, exists, err := getQueueURLByName(ctx, sqsCli, queueName)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("stream queue created but url lookup failed")
		}
		url = resolved
	}
	return url, nil
}

func getQueueARNAndPolicy(ctx context.Context, sqsCli sqsAPI, queueURL string) (string, string, error) {
	q := strings.TrimSpace(queueURL)
	if q == "" {
		return "", "", fmt.Errorf("empty queue url")
	}
	out, err := sqsCli.GetQueueAttributes(ctx, &sdksqs.GetQueueAttributesInput{
		QueueUrl: &q,
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameQueueArn,
			sqstypes.QueueAttributeNamePolicy,
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("get stream queue attributes: %w", err)
	}
	arn := strings.TrimSpace(out.Attributes[string(sqstypes.QueueAttributeNameQueueArn)])
	if arn == "" {
		return "", "", fmt.Errorf("stream queue arn not available")
	}
	policy := strings.TrimSpace(out.Attributes[string(sqstypes.QueueAttributeNamePolicy)])
	return arn, policy, nil
}

func ensureQueuePolicyForTopic(ctx context.Context, sqsCli sqsAPI, queueURL, queueARN, existingPolicy, topicARN string) (bool, error) {
	doc, err := parsePolicy(existingPolicy)
	if err != nil {
		return false, fmt.Errorf("parse stream queue policy: %w", err)
	}
	stmtRaw, _ := doc["Statement"].([]any)
	statements := stmtRaw
	if !hasPolicyStatement(statements, queueARN, topicARN) {
		statements = append(statements, map[string]any{
			"Sid":       "AwscopeSNSStreamAllow" + shortHash(topicARN),
			"Effect":    "Allow",
			"Principal": map[string]any{"Service": "sns.amazonaws.com"},
			"Action":    "sqs:SendMessage",
			"Resource":  queueARN,
			"Condition": map[string]any{"ArnEquals": map[string]any{"aws:SourceArn": topicARN}},
		})
		doc["Statement"] = statements
		if _, ok := doc["Version"]; !ok {
			doc["Version"] = "2012-10-17"
		}
		blob, err := json.Marshal(doc)
		if err != nil {
			return false, fmt.Errorf("marshal stream queue policy: %w", err)
		}
		q := strings.TrimSpace(queueURL)
		_, err = sqsCli.SetQueueAttributes(ctx, &sdksqs.SetQueueAttributesInput{
			QueueUrl: &q,
			Attributes: map[string]string{
				string(sqstypes.QueueAttributeNamePolicy): string(blob),
			},
		})
		if err != nil {
			return false, fmt.Errorf("set stream queue policy: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func ensureTopicSubscription(ctx context.Context, snsCli snsAPI, topicARN, queueARN string) (string, bool, error) {
	if existing, ok, err := findTopicSubscription(ctx, snsCli, topicARN, queueARN); err != nil {
		return "", false, err
	} else if ok {
		return existing, false, nil
	}

	attrs := map[string]string{"RawMessageDelivery": "true"}
	out, err := snsCli.Subscribe(ctx, &sdksns.SubscribeInput{
		TopicArn:              &topicARN,
		Protocol:              awsSDK.String("sqs"),
		Endpoint:              &queueARN,
		Attributes:            attrs,
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		return "", false, fmt.Errorf("subscribe queue to topic %s: %w", topicARN, err)
	}
	return strings.TrimSpace(awsSDK.ToString(out.SubscriptionArn)), true, nil
}

func findTopicSubscription(ctx context.Context, snsCli snsAPI, topicARN, queueARN string) (string, bool, error) {
	var token *string
	for {
		out, err := snsCli.ListSubscriptionsByTopic(ctx, &sdksns.ListSubscriptionsByTopicInput{TopicArn: &topicARN, NextToken: token})
		if err != nil {
			return "", false, fmt.Errorf("list subscriptions for topic %s: %w", topicARN, err)
		}
		for _, sub := range out.Subscriptions {
			if !strings.EqualFold(strings.TrimSpace(awsSDK.ToString(sub.Protocol)), "sqs") {
				continue
			}
			if strings.TrimSpace(awsSDK.ToString(sub.Endpoint)) != queueARN {
				continue
			}
			return strings.TrimSpace(awsSDK.ToString(sub.SubscriptionArn)), true, nil
		}
		if out.NextToken == nil || strings.TrimSpace(awsSDK.ToString(out.NextToken)) == "" {
			break
		}
		token = out.NextToken
	}
	return "", false, nil
}

type streamStats struct {
	Streamed int
	Deleted  int
	Polls    int
}

func streamTopicMessages(ctx context.Context, sqsCli sqsAPI, queueURL, topicARN string, out io.Writer, errOut io.Writer) (streamStats, error) {
	stats := streamStats{}
	for {
		select {
		case <-ctx.Done():
			return stats, nil
		default:
		}

		q := queueURL
		resp, err := sqsCli.ReceiveMessage(ctx, &sdksqs.ReceiveMessageInput{
			QueueUrl:            &q,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20,
			MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{
				sqstypes.MessageSystemAttributeNameSentTimestamp,
				sqstypes.MessageSystemAttributeNameApproximateReceiveCount,
				sqstypes.MessageSystemAttributeNameMessageGroupId,
				sqstypes.MessageSystemAttributeNameSequenceNumber,
			},
			MessageAttributeNames: []string{"All"},
		})
		if err != nil {
			if ctx.Err() != nil {
				return stats, nil
			}
			fmt.Fprintf(errOut, "stream receive error: %v\n", err)
			time.Sleep(2 * time.Second)
			continue
		}
		stats.Polls++
		if len(resp.Messages) == 0 {
			continue
		}

		delEntries := make([]sqstypes.DeleteMessageBatchRequestEntry, 0, len(resp.Messages))
		for i, msg := range resp.Messages {
			fmt.Fprintln(out, formatMessageLine(msg, topicARN))
			stats.Streamed++
			receipt := strings.TrimSpace(awsSDK.ToString(msg.ReceiptHandle))
			if receipt == "" {
				continue
			}
			delEntries = append(delEntries, sqstypes.DeleteMessageBatchRequestEntry{Id: awsSDK.String(fmt.Sprintf("m%d", i)), ReceiptHandle: &receipt})
		}
		if len(delEntries) == 0 {
			continue
		}
		delOut, err := sqsCli.DeleteMessageBatch(ctx, &sdksqs.DeleteMessageBatchInput{QueueUrl: &q, Entries: delEntries})
		if err != nil {
			fmt.Fprintf(errOut, "stream delete error: %v\n", err)
			continue
		}
		stats.Deleted += len(delEntries) - len(delOut.Failed)
	}
}

func formatMessageLine(msg sqstypes.Message, topicARN string) string {
	msgID := strings.TrimSpace(awsSDK.ToString(msg.MessageId))
	if msgID == "" {
		msgID = "-"
	}
	body := strings.TrimSpace(awsSDK.ToString(msg.Body))
	topic := topicARN
	subject := ""
	if t, s, b, ok := parseSNSEnvelope(body); ok {
		if t != "" {
			topic = t
		}
		subject = s
		if strings.TrimSpace(b) != "" {
			body = b
		}
	}
	rc := strings.TrimSpace(msg.Attributes[string(sqstypes.MessageSystemAttributeNameApproximateReceiveCount)])
	if rc == "" {
		rc = "1"
	}
	ts := sentTimestamp(msg.Attributes[string(sqstypes.MessageSystemAttributeNameSentTimestamp)])
	parts := []string{fmt.Sprintf("[%s]", ts), "id=" + msgID, "rc=" + rc, "topic=" + topicNameFromARN(topic)}
	if strings.TrimSpace(subject) != "" {
		parts = append(parts, "subject="+subject)
	}
	head := strings.Join(parts, " ")
	return strings.Join([]string{head, "msg:", formatPayloadForDisplay(body), ""}, "\n")
}

func parseSNSEnvelope(body string) (topicARN, subject, message string, ok bool) {
	var env struct {
		TopicArn string `json:"TopicArn"`
		Subject  string `json:"Subject"`
		Message  string `json:"Message"`
	}
	if !json.Valid([]byte(body)) {
		return "", "", "", false
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return "", "", "", false
	}
	if strings.TrimSpace(env.TopicArn) == "" {
		return "", "", "", false
	}
	return strings.TrimSpace(env.TopicArn), strings.TrimSpace(env.Subject), env.Message, true
}

func parsePolicy(in string) (map[string]any, error) {
	if strings.TrimSpace(in) == "" {
		return map[string]any{"Version": "2012-10-17", "Statement": []any{}}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		return nil, err
	}
	if _, ok := doc["Statement"]; !ok {
		doc["Statement"] = []any{}
	}
	return doc, nil
}

func hasPolicyStatement(statements []any, queueARN, topicARN string) bool {
	for _, raw := range statements {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(asString(m["Action"])) != "sqs:SendMessage" {
			continue
		}
		if strings.TrimSpace(asString(m["Resource"])) != queueARN {
			continue
		}
		cond, _ := m["Condition"].(map[string]any)
		arnEq, _ := cond["ArnEquals"].(map[string]any)
		if strings.TrimSpace(asString(arnEq["aws:SourceArn"])) == topicARN {
			return true
		}
	}
	return false
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return ""
	}
}

func sentTimestamp(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Now().UTC().Format("2006-01-02 15:04:05")
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05")
}

func formatPayloadForDisplay(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	if !json.Valid([]byte(s)) {
		return s
	}
	var doc any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return s
	}
	doc = expandNestedJSONStrings(doc, 0)
	pretty, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return s
	}
	return string(pretty)
}

func isOwnedActionQueue(ctx context.Context, sqsCli sqsAPI, queueURL string, expectedTags map[string]string) (bool, error) {
	q := strings.TrimSpace(queueURL)
	if q == "" {
		return false, nil
	}
	out, err := sqsCli.ListQueueTags(ctx, &sdksqs.ListQueueTagsInput{QueueUrl: &q})
	if err != nil {
		return false, err
	}
	tags := out.Tags
	if !strings.EqualFold(strings.TrimSpace(tags["awscope:managed_by"]), "awscope") {
		return false, nil
	}
	for k, want := range expectedTags {
		want = strings.TrimSpace(want)
		if want == "" {
			continue
		}
		if strings.TrimSpace(tags[k]) != want {
			return false, nil
		}
	}
	return true, nil
}

func expandNestedJSONStrings(v any, depth int) any {
	if depth >= 4 {
		return v
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = expandNestedJSONStrings(vv, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = expandNestedJSONStrings(x[i], depth+1)
		}
		return out
	case string:
		s := strings.TrimSpace(x)
		if s == "" || (!strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[")) {
			return x
		}
		if !json.Valid([]byte(s)) {
			return x
		}
		var nested any
		if err := json.Unmarshal([]byte(s), &nested); err != nil {
			return x
		}
		return expandNestedJSONStrings(nested, depth+1)
	default:
		return v
	}
}

func topicNameFromARN(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 && i+1 < len(arn) {
		return arn[i+1:]
	}
	return arn
}

func streamQueueName(topicName, topicARN string, fifo bool) string {
	base := strings.TrimSpace(topicName)
	if base == "" {
		base = topicNameFromARN(topicARN)
	}
	base = strings.TrimSuffix(base, ".fifo")
	candidate := base + "-awscope-stream"
	suffix := ""
	if fifo {
		suffix = ".fifo"
	}
	maxLen := 80 - len(suffix)
	if len(candidate) > maxLen {
		h := shortHash(topicARN)
		keep := maxLen - len("-s-") - len(h)
		if keep < 1 {
			keep = 1
		}
		candidate = candidate[:keep] + "-s-" + h
	}
	return candidate + suffix
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func isSQSErrorCode(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	code := strings.TrimSpace(apiErr.ErrorCode())
	for _, c := range codes {
		if code == c {
			return true
		}
	}
	return false
}

func requireRegional(region string) error {
	r := strings.TrimSpace(region)
	if r == "" || r == "global" {
		return fmt.Errorf("action requires a regional resource, got region=%q", region)
	}
	return nil
}

type prompter struct {
	reader *bufio.Reader
	out    io.Writer
}

func newPrompter(in io.Reader, out io.Writer) *prompter {
	if in == nil {
		return &prompter{out: out}
	}
	return &prompter{reader: bufio.NewReader(in), out: out}
}

func (p *prompter) confirmStep(step, total int, desc string) (bool, error) {
	return p.confirm(fmt.Sprintf("[step %d/%d] %s. Proceed? [y/N]: ", step, total, desc))
}

func (p *prompter) confirmTeardown(step, total int, desc string) (bool, error) {
	return p.confirm(fmt.Sprintf("[teardown %d/%d] %s. Proceed? [y/N]: ", step, total, desc))
}

func (p *prompter) confirm(prompt string) (bool, error) {
	if p == nil || p.reader == nil {
		return false, fmt.Errorf("interactive confirmation required")
	}
	if _, err := fmt.Fprint(nonNilWriter(p.out, os.Stdout), prompt); err != nil {
		return false, err
	}
	line, err := p.reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			if strings.TrimSpace(line) == "" {
				return false, fmt.Errorf("interactive confirmation required")
			}
		} else {
			return false, fmt.Errorf("interactive confirmation required: %w", err)
		}
	}
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes", nil
}

type sessionState struct {
	queueCreated            bool
	queueDeleteOnExit       bool
	queueName               string
	queueURL                string
	queueARN                string
	topicARN                string
	createdSubscriptionARNs []string
	policyPatched           bool
}

func (s sessionState) needsTeardown() bool {
	return s.queueDeleteOnExit || len(s.createdSubscriptionARNs) > 0
}

type teardownSummary struct {
	Unsubscribed       int
	SkippedUnsubscribe int
	QueueDeleted       bool
	QueueDeleteSkipped bool
	Retained           []string
}

func (t teardownSummary) toMap() map[string]any {
	return map[string]any{
		"unsubscribed":         t.Unsubscribed,
		"skipped_unsubscribe":  t.SkippedUnsubscribe,
		"queue_deleted":        t.QueueDeleted,
		"queue_delete_skipped": t.QueueDeleteSkipped,
		"retained":             t.Retained,
	}
}

func teardownSession(ctx context.Context, prompt *prompter, sqsCli sqsAPI, snsCli snsAPI, execCtx actions.ExecContext, state *sessionState, out io.Writer, errOut io.Writer, autoApprove bool) (teardownSummary, error) {
	summary := teardownSummary{}
	total := 0
	if len(state.createdSubscriptionARNs) > 0 {
		total++
	}
	if state.queueDeleteOnExit {
		total++
	}
	if total == 0 {
		return summary, nil
	}
	step := 0

	if len(state.createdSubscriptionARNs) > 0 {
		step++
		ok := true
		if !autoApprove {
			var err error
			ok, err = prompt.confirmTeardown(step, total, fmt.Sprintf("unsubscribe %d session-created subscription(s)", len(state.createdSubscriptionARNs)))
			if err != nil {
				return summary, err
			}
		}
		if !ok {
			summary.SkippedUnsubscribe = len(state.createdSubscriptionARNs)
			summary.Retained = append(summary.Retained, fmt.Sprintf("%d subscription(s)", len(state.createdSubscriptionARNs)))
			printUnsubscribeCleanup(execCtx, state.createdSubscriptionARNs, out)
		} else {
			for _, subARN := range state.createdSubscriptionARNs {
				subARN = strings.TrimSpace(subARN)
				if subARN == "" {
					continue
				}
				_, err := snsCli.Unsubscribe(ctx, &sdksns.UnsubscribeInput{SubscriptionArn: &subARN})
				if err != nil {
					return summary, fmt.Errorf("unsubscribe %s: %w", subARN, err)
				}
				summary.Unsubscribed++
			}
		}
	}

	if state.queueDeleteOnExit {
		step++
		ok := true
		desc := fmt.Sprintf("delete stream queue %s", state.queueName)
		if state.queueCreated {
			desc = fmt.Sprintf("delete session-created stream queue %s", state.queueName)
		} else {
			desc = fmt.Sprintf("delete awscope-managed stream queue %s", state.queueName)
		}
		if !autoApprove {
			var err error
			ok, err = prompt.confirmTeardown(step, total, desc)
			if err != nil {
				return summary, err
			}
		}
		if !ok {
			summary.QueueDeleteSkipped = true
			summary.Retained = append(summary.Retained, "stream queue")
			printDeleteQueueCleanup(execCtx, state.queueURL, out)
		} else {
			q := state.queueURL
			_, err := sqsCli.DeleteQueue(ctx, &sdksqs.DeleteQueueInput{QueueUrl: &q})
			if err != nil {
				return summary, fmt.Errorf("delete stream queue: %w", err)
			}
			summary.QueueDeleted = true
		}
	}

	if len(summary.Retained) > 0 {
		fmt.Fprintf(errOut, "teardown retained resources: %s\n", strings.Join(summary.Retained, ", "))
	}
	return summary, nil
}

func printUnsubscribeCleanup(execCtx actions.ExecContext, subARNs []string, out io.Writer) {
	for _, subARN := range subARNs {
		subARN = strings.TrimSpace(subARN)
		if subARN == "" {
			continue
		}
		fmt.Fprintf(out, "cleanup: aws sns unsubscribe --subscription-arn %s --region %s%s\n", subARN, execCtx.Region, profileArg(execCtx.Profile))
	}
}

func printDeleteQueueCleanup(execCtx actions.ExecContext, queueURL string, out io.Writer) {
	fmt.Fprintf(out, "cleanup: aws sqs delete-queue --queue-url %s --region %s%s\n", strings.TrimSpace(queueURL), execCtx.Region, profileArg(execCtx.Profile))
}

func profileArg(profile string) string {
	p := strings.TrimSpace(profile)
	if p == "" {
		return ""
	}
	return " --profile " + p
}

func nonNilReader(v io.Reader, fallback io.Reader) io.Reader {
	if v != nil {
		return v
	}
	return fallback
}

func nonNilWriter(v io.Writer, fallback io.Writer) io.Writer {
	if v != nil {
		return v
	}
	return fallback
}
