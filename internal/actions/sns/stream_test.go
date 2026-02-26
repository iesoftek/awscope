package sns

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"awscope/internal/actions"
	"awscope/internal/graph"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	sdksns "github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	sdksqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"
)

func TestSNSStreamApplicable(t *testing.T) {
	a := Stream{}
	if !a.Applicable(graph.ResourceNode{Service: "sns", Type: "sns:topic", Arn: "arn:aws:sns:us-east-1:123456789012:events"}) {
		t.Fatalf("expected sns topic arn to be applicable")
	}
	if a.Applicable(graph.ResourceNode{Service: "sns", Type: "sns:topic", PrimaryID: "events"}) {
		t.Fatalf("expected topic without arn to be inapplicable")
	}
	if a.Applicable(graph.ResourceNode{Service: "sqs", Type: "sqs:queue", Arn: "arn:aws:sqs:us-east-1:123456789012:q"}) {
		t.Fatalf("expected non-sns resource to be inapplicable")
	}
}

func TestStreamQueueName(t *testing.T) {
	name := streamQueueName("events", "arn:aws:sns:us-east-1:123456789012:events", false)
	if name != "events-awscope-stream" {
		t.Fatalf("unexpected stream queue name: %s", name)
	}
	fifo := streamQueueName("events.fifo", "arn:aws:sns:us-east-1:123456789012:events.fifo", true)
	if !strings.HasSuffix(fifo, ".fifo") {
		t.Fatalf("fifo stream queue name must end with .fifo: %s", fifo)
	}
	long := strings.Repeat("x", 160)
	short := streamQueueName(long, "arn:aws:sns:us-east-1:123456789012:"+long, false)
	if len(short) > 80 {
		t.Fatalf("stream queue name must be <= 80 chars: len=%d", len(short))
	}
}

func TestSNSStreamNonInteractiveConfirmationFails(t *testing.T) {
	oldSNS, oldSQS := newSNSActionClient, newSQSActionClient
	t.Cleanup(func() {
		newSNSActionClient = oldSNS
		newSQSActionClient = oldSQS
	})

	topicARN := "arn:aws:sns:us-east-1:123456789012:events"
	fSNS := &fakeSNS{topicAttrs: map[string]map[string]string{topicARN: {"FifoTopic": "false"}}}
	fSQS := newFakeSQS("us-east-1", "123456789012")

	newSNSActionClient = func(cfg awsSDK.Config) snsAPI { return fSNS }
	newSQSActionClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }

	a := Stream{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region: "us-east-1",
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}, graph.ResourceNode{Service: "sns", Type: "sns:topic", Arn: topicARN})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive confirmation required") {
		t.Fatalf("expected interactive confirmation error, got %v", err)
	}
}

func TestSNSStreamRejectBeforeQueueCreate(t *testing.T) {
	oldSNS, oldSQS := newSNSActionClient, newSQSActionClient
	t.Cleanup(func() {
		newSNSActionClient = oldSNS
		newSQSActionClient = oldSQS
	})

	topicARN := "arn:aws:sns:us-east-1:123456789012:events"
	fSNS := &fakeSNS{topicAttrs: map[string]map[string]string{topicARN: {"FifoTopic": "false"}}}
	fSQS := newFakeSQS("us-east-1", "123456789012")

	newSNSActionClient = func(cfg awsSDK.Config) snsAPI { return fSNS }
	newSQSActionClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }

	a := Stream{}
	_, err := a.ExecuteTerminal(context.Background(), actions.ExecContext{
		Region: "us-east-1",
		Stdin:  strings.NewReader("y\nn\n"),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}, graph.ResourceNode{Service: "sns", Type: "sns:topic", Arn: topicARN})
	if err == nil || !strings.Contains(err.Error(), "aborted by user before queue creation") {
		t.Fatalf("expected abort before queue creation, got %v", err)
	}
	if len(fSQS.createdQueues) != 0 {
		t.Fatalf("expected no queue creation")
	}
}

func TestSNSStreamCreatesFIFOQueueAndTeardown(t *testing.T) {
	oldSNS, oldSQS := newSNSActionClient, newSQSActionClient
	t.Cleanup(func() {
		newSNSActionClient = oldSNS
		newSQSActionClient = oldSQS
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	topicARN := "arn:aws:sns:us-east-1:123456789012:orders.fifo"
	fSNS := &fakeSNS{
		topicAttrs: map[string]map[string]string{topicARN: {"FifoTopic": "true"}},
		subsByTopic: map[string][]snstypes.Subscription{
			topicARN: {},
		},
	}
	fSQS := newFakeSQS("us-east-1", "123456789012")
	fSQS.receiveOutputs = []*sdksqs.ReceiveMessageOutput{{
		Messages: []sqstypes.Message{{
			MessageId:     awsSDK.String("m-1"),
			ReceiptHandle: awsSDK.String("r-1"),
			Body:          awsSDK.String("hello sns fifo"),
			Attributes: map[string]string{
				string(sqstypes.MessageSystemAttributeNameApproximateReceiveCount): "1",
				string(sqstypes.MessageSystemAttributeNameSentTimestamp):           "1730000000000",
			},
		}},
	}}
	fSQS.onReceive = func() { cancel() }

	newSNSActionClient = func(cfg awsSDK.Config) snsAPI { return fSNS }
	newSQSActionClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }

	in := strings.NewReader("y\ny\ny\ny\ny\ny\ny\n")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	a := Stream{}
	res, err := a.ExecuteTerminal(ctx, actions.ExecContext{Region: "us-east-1", Profile: "default", Stdin: in, Stdout: stdout, Stderr: stderr}, graph.ResourceNode{Service: "sns", Type: "sns:topic", Arn: topicARN})
	if err != nil {
		t.Fatalf("ExecuteTerminal: %v", err)
	}
	if len(fSQS.createdQueues) != 1 {
		t.Fatalf("expected one queue created, got %d", len(fSQS.createdQueues))
	}
	createdName := fSQS.createdQueues[0]
	if !strings.HasSuffix(createdName, ".fifo") {
		t.Fatalf("expected fifo queue name, got %s", createdName)
	}
	attrs := fSQS.createdQueueAttrs[createdName]
	if attrs[string(sqstypes.QueueAttributeNameFifoQueue)] != "true" {
		t.Fatalf("expected FifoQueue=true attrs=%v", attrs)
	}
	if attrs[string(sqstypes.QueueAttributeNameContentBasedDeduplication)] != "true" {
		t.Fatalf("expected ContentBasedDeduplication=true attrs=%v", attrs)
	}
	if len(fSNS.createdSubs) != 1 {
		t.Fatalf("expected one created sub, got %d", len(fSNS.createdSubs))
	}
	if len(fSNS.unsubscribed) != 1 {
		t.Fatalf("expected one unsubscribe, got %d", len(fSNS.unsubscribed))
	}
	if len(fSQS.deletedQueues) != 1 {
		t.Fatalf("expected one queue deletion, got %d", len(fSQS.deletedQueues))
	}
	if !strings.Contains(stdout.String(), "streaming from") {
		t.Fatalf("expected streaming output, got %q", stdout.String())
	}
	if res.Data == nil || res.Data["queue_created"] != true {
		t.Fatalf("expected queue_created=true, got %#v", res.Data)
	}
}

func TestSNSStreamKeepsPreExistingQueueAndSubscription(t *testing.T) {
	oldSNS, oldSQS := newSNSActionClient, newSQSActionClient
	t.Cleanup(func() {
		newSNSActionClient = oldSNS
		newSQSActionClient = oldSQS
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	topicARN := "arn:aws:sns:us-east-1:123456789012:events"
	queueName := streamQueueName("events", topicARN, false)
	queueURL := "https://sqs.us-east-1.amazonaws.com/123456789012/" + queueName
	queueARN := "arn:aws:sqs:us-east-1:123456789012:" + queueName

	fSNS := &fakeSNS{
		topicAttrs: map[string]map[string]string{topicARN: {"FifoTopic": "false"}},
		subsByTopic: map[string][]snstypes.Subscription{
			topicARN: {{
				Protocol:        awsSDK.String("sqs"),
				Endpoint:        awsSDK.String(queueARN),
				SubscriptionArn: awsSDK.String("arn:aws:sns:us-east-1:123456789012:sub/existing"),
			}},
		},
	}
	fSQS := newFakeSQS("us-east-1", "123456789012")
	fSQS.addQueue(queueName, queueURL, queueARN, false)
	fSQS.receiveOutputs = []*sdksqs.ReceiveMessageOutput{{}}
	fSQS.onReceive = func() { cancel() }

	newSNSActionClient = func(cfg awsSDK.Config) snsAPI { return fSNS }
	newSQSActionClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }

	in := strings.NewReader("y\ny\ny\ny\n")
	a := Stream{}
	_, err := a.ExecuteTerminal(ctx, actions.ExecContext{Region: "us-east-1", Profile: "default", Stdin: in, Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, graph.ResourceNode{Service: "sns", Type: "sns:topic", Arn: topicARN})
	if err != nil {
		t.Fatalf("ExecuteTerminal: %v", err)
	}
	if len(fSQS.createdQueues) != 0 {
		t.Fatalf("expected no queue creation")
	}
	if len(fSNS.createdSubs) != 0 {
		t.Fatalf("expected no new subscription")
	}
	if len(fSNS.unsubscribed) != 0 {
		t.Fatalf("expected no unsubscribe for pre-existing subscription")
	}
	if len(fSQS.deletedQueues) != 0 {
		t.Fatalf("expected no queue delete for pre-existing queue")
	}
}

func TestSNSStreamDeletesPreExistingAwscopeManagedQueueOnCancel(t *testing.T) {
	oldSNS, oldSQS := newSNSActionClient, newSQSActionClient
	t.Cleanup(func() {
		newSNSActionClient = oldSNS
		newSQSActionClient = oldSQS
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	topicARN := "arn:aws:sns:us-east-1:123456789012:events"
	queueName := streamQueueName("events", topicARN, false)
	queueURL := "https://sqs.us-east-1.amazonaws.com/123456789012/" + queueName
	queueARN := "arn:aws:sqs:us-east-1:123456789012:" + queueName

	fSNS := &fakeSNS{
		topicAttrs: map[string]map[string]string{topicARN: {"FifoTopic": "false"}},
		subsByTopic: map[string][]snstypes.Subscription{
			topicARN: {{
				Protocol:        awsSDK.String("sqs"),
				Endpoint:        awsSDK.String(queueARN),
				SubscriptionArn: awsSDK.String("arn:aws:sns:us-east-1:123456789012:sub/existing"),
			}},
		},
	}
	fSQS := newFakeSQS("us-east-1", "123456789012")
	fSQS.addQueue(queueName, queueURL, queueARN, false)
	fSQS.queueTags[queueURL] = map[string]string{
		"awscope:managed_by":       "awscope",
		"awscope:stream_for_topic": topicARN,
	}
	fSQS.receiveOutputs = []*sdksqs.ReceiveMessageOutput{{}}
	fSQS.onReceive = func() { cancel() }

	newSNSActionClient = func(cfg awsSDK.Config) snsAPI { return fSNS }
	newSQSActionClient = func(cfg awsSDK.Config) sqsAPI { return fSQS }

	in := strings.NewReader("y\ny\ny\ny\n")
	a := Stream{}
	_, err := a.ExecuteTerminal(ctx, actions.ExecContext{
		Region:                      "us-east-1",
		Profile:                     "default",
		Stdin:                       in,
		Stdout:                      &bytes.Buffer{},
		Stderr:                      &bytes.Buffer{},
		AutoApproveTeardownOnCancel: true,
	}, graph.ResourceNode{Service: "sns", Type: "sns:topic", Arn: topicARN})
	if err != nil {
		t.Fatalf("ExecuteTerminal: %v", err)
	}
	if len(fSQS.deletedQueues) != 1 {
		t.Fatalf("expected managed queue deleted on cancel, got %d", len(fSQS.deletedQueues))
	}
}

func TestTeardownSessionAutoApproveOnCancel(t *testing.T) {
	fSQS := newFakeSQS("us-east-1", "123456789012")
	fSNS := &fakeSNS{subsByTopic: map[string][]snstypes.Subscription{}}
	state := &sessionState{
		queueCreated:            true,
		queueDeleteOnExit:       true,
		queueName:               "events-awscope-stream",
		queueURL:                "https://sqs.us-east-1.amazonaws.com/123456789012/events-awscope-stream",
		createdSubscriptionARNs: []string{"arn:aws:sns:us-east-1:123456789012:sub/1"},
	}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	prompt := newPrompter(nil, out)

	sum, err := teardownSession(context.Background(), prompt, fSQS, fSNS, actions.ExecContext{
		Region:  "us-east-1",
		Profile: "default",
	}, state, out, errOut, true)
	if err != nil {
		t.Fatalf("teardownSession: %v", err)
	}
	if sum.Unsubscribed != 1 {
		t.Fatalf("expected one unsubscribe, got %d", sum.Unsubscribed)
	}
	if !sum.QueueDeleted {
		t.Fatalf("expected queue delete")
	}
	if len(fSNS.unsubscribed) != 1 {
		t.Fatalf("expected one unsubscribed arn, got %d", len(fSNS.unsubscribed))
	}
	if len(fSQS.deletedQueues) != 1 {
		t.Fatalf("expected one deleted queue, got %d", len(fSQS.deletedQueues))
	}
}

func TestTeardownSessionPromptRequiredByDefault(t *testing.T) {
	fSQS := newFakeSQS("us-east-1", "123456789012")
	fSNS := &fakeSNS{subsByTopic: map[string][]snstypes.Subscription{}}
	state := &sessionState{
		queueCreated:            true,
		queueDeleteOnExit:       true,
		queueName:               "events-awscope-stream",
		queueURL:                "https://sqs.us-east-1.amazonaws.com/123456789012/events-awscope-stream",
		createdSubscriptionARNs: []string{"arn:aws:sns:us-east-1:123456789012:sub/1"},
	}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	prompt := newPrompter(nil, out)

	_, err := teardownSession(context.Background(), prompt, fSQS, fSNS, actions.ExecContext{
		Region:  "us-east-1",
		Profile: "default",
	}, state, out, errOut, false)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "interactive confirmation required") {
		t.Fatalf("expected interactive confirmation error, got %v", err)
	}
}

type fakeSQS struct {
	region string
	acct   string

	queueURLByName map[string]string
	queueAttrs     map[string]map[string]string
	queueTags      map[string]map[string]string

	createdQueues     []string
	createdQueueAttrs map[string]map[string]string
	deletedQueues     []string
	deletedBatches    int

	receiveOutputs []*sdksqs.ReceiveMessageOutput
	onReceive      func()
}

func newFakeSQS(region, acct string) *fakeSQS {
	return &fakeSQS{
		region:            region,
		acct:              acct,
		queueURLByName:    map[string]string{},
		queueAttrs:        map[string]map[string]string{},
		queueTags:         map[string]map[string]string{},
		createdQueueAttrs: map[string]map[string]string{},
	}
}

func (f *fakeSQS) addQueue(name, url, arn string, fifo bool) {
	f.queueURLByName[name] = url
	f.queueAttrs[url] = map[string]string{
		string(sqstypes.QueueAttributeNameQueueArn): arn,
		string(sqstypes.QueueAttributeNameFifoQueue): func() string {
			if fifo {
				return "true"
			}
			return "false"
		}(),
		string(sqstypes.QueueAttributeNamePolicy): "",
	}
	f.queueTags[url] = map[string]string{}
}

func (f *fakeSQS) GetQueueAttributes(ctx context.Context, params *sdksqs.GetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueAttributesOutput, error) {
	attrs, ok := f.queueAttrs[awsSDK.ToString(params.QueueUrl)]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "missing queue"}
	}
	out := map[string]string{}
	for k, v := range attrs {
		out[k] = v
	}
	return &sdksqs.GetQueueAttributesOutput{Attributes: out}, nil
}

func (f *fakeSQS) GetQueueUrl(ctx context.Context, params *sdksqs.GetQueueUrlInput, optFns ...func(*sdksqs.Options)) (*sdksqs.GetQueueUrlOutput, error) {
	name := awsSDK.ToString(params.QueueName)
	url, ok := f.queueURLByName[name]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "not found"}
	}
	return &sdksqs.GetQueueUrlOutput{QueueUrl: awsSDK.String(url)}, nil
}

func (f *fakeSQS) CreateQueue(ctx context.Context, params *sdksqs.CreateQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.CreateQueueOutput, error) {
	name := awsSDK.ToString(params.QueueName)
	url := fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", f.region, f.acct, name)
	arn := fmt.Sprintf("arn:aws:sqs:%s:%s:%s", f.region, f.acct, name)
	f.queueURLByName[name] = url
	attrs := map[string]string{string(sqstypes.QueueAttributeNameQueueArn): arn}
	for k, v := range params.Attributes {
		attrs[k] = v
	}
	if _, ok := attrs[string(sqstypes.QueueAttributeNamePolicy)]; !ok {
		attrs[string(sqstypes.QueueAttributeNamePolicy)] = ""
	}
	f.queueAttrs[url] = attrs
	tagCopy := map[string]string{}
	for k, v := range params.Tags {
		tagCopy[k] = v
	}
	f.queueTags[url] = tagCopy
	f.createdQueues = append(f.createdQueues, name)
	cpy := map[string]string{}
	for k, v := range params.Attributes {
		cpy[k] = v
	}
	f.createdQueueAttrs[name] = cpy
	return &sdksqs.CreateQueueOutput{QueueUrl: awsSDK.String(url)}, nil
}

func (f *fakeSQS) ListQueueTags(ctx context.Context, params *sdksqs.ListQueueTagsInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ListQueueTagsOutput, error) {
	url := awsSDK.ToString(params.QueueUrl)
	tags, ok := f.queueTags[url]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "missing queue"}
	}
	out := map[string]string{}
	for k, v := range tags {
		out[k] = v
	}
	return &sdksqs.ListQueueTagsOutput{Tags: out}, nil
}

func (f *fakeSQS) SetQueueAttributes(ctx context.Context, params *sdksqs.SetQueueAttributesInput, optFns ...func(*sdksqs.Options)) (*sdksqs.SetQueueAttributesOutput, error) {
	url := awsSDK.ToString(params.QueueUrl)
	attrs, ok := f.queueAttrs[url]
	if !ok {
		return nil, fakeAPIError{code: "AWS.SimpleQueueService.NonExistentQueue", msg: "missing queue"}
	}
	for k, v := range params.Attributes {
		attrs[k] = v
	}
	f.queueAttrs[url] = attrs
	return &sdksqs.SetQueueAttributesOutput{}, nil
}

func (f *fakeSQS) ReceiveMessage(ctx context.Context, params *sdksqs.ReceiveMessageInput, optFns ...func(*sdksqs.Options)) (*sdksqs.ReceiveMessageOutput, error) {
	if f.onReceive != nil {
		f.onReceive()
		f.onReceive = nil
	}
	if len(f.receiveOutputs) == 0 {
		return &sdksqs.ReceiveMessageOutput{}, nil
	}
	out := f.receiveOutputs[0]
	f.receiveOutputs = f.receiveOutputs[1:]
	return out, nil
}

func (f *fakeSQS) DeleteMessageBatch(ctx context.Context, params *sdksqs.DeleteMessageBatchInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteMessageBatchOutput, error) {
	f.deletedBatches++
	return &sdksqs.DeleteMessageBatchOutput{}, nil
}

func (f *fakeSQS) DeleteQueue(ctx context.Context, params *sdksqs.DeleteQueueInput, optFns ...func(*sdksqs.Options)) (*sdksqs.DeleteQueueOutput, error) {
	f.deletedQueues = append(f.deletedQueues, awsSDK.ToString(params.QueueUrl))
	return &sdksqs.DeleteQueueOutput{}, nil
}

type fakeSNS struct {
	topicAttrs   map[string]map[string]string
	subsByTopic  map[string][]snstypes.Subscription
	createdSubs  []string
	unsubscribed []string
}

func (f *fakeSNS) GetTopicAttributes(ctx context.Context, params *sdksns.GetTopicAttributesInput, optFns ...func(*sdksns.Options)) (*sdksns.GetTopicAttributesOutput, error) {
	topic := strings.TrimSpace(awsSDK.ToString(params.TopicArn))
	attrs, ok := f.topicAttrs[topic]
	if !ok {
		return nil, fakeAPIError{code: "NotFound", msg: "topic not found"}
	}
	out := map[string]string{}
	for k, v := range attrs {
		out[k] = v
	}
	return &sdksns.GetTopicAttributesOutput{Attributes: out}, nil
}

func (f *fakeSNS) ListSubscriptionsByTopic(ctx context.Context, params *sdksns.ListSubscriptionsByTopicInput, optFns ...func(*sdksns.Options)) (*sdksns.ListSubscriptionsByTopicOutput, error) {
	topic := strings.TrimSpace(awsSDK.ToString(params.TopicArn))
	return &sdksns.ListSubscriptionsByTopicOutput{Subscriptions: append([]snstypes.Subscription(nil), f.subsByTopic[topic]...)}, nil
}

func (f *fakeSNS) Subscribe(ctx context.Context, params *sdksns.SubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.SubscribeOutput, error) {
	topic := strings.TrimSpace(awsSDK.ToString(params.TopicArn))
	endpoint := strings.TrimSpace(awsSDK.ToString(params.Endpoint))
	arn := fmt.Sprintf("arn:aws:sns:us-east-1:123456789012:sub/%d", len(f.createdSubs)+1)
	f.createdSubs = append(f.createdSubs, arn)
	sub := snstypes.Subscription{
		SubscriptionArn: awsSDK.String(arn),
		TopicArn:        awsSDK.String(topic),
		Endpoint:        awsSDK.String(endpoint),
		Protocol:        awsSDK.String("sqs"),
	}
	f.subsByTopic[topic] = append(f.subsByTopic[topic], sub)
	return &sdksns.SubscribeOutput{SubscriptionArn: awsSDK.String(arn)}, nil
}

func (f *fakeSNS) Unsubscribe(ctx context.Context, params *sdksns.UnsubscribeInput, optFns ...func(*sdksns.Options)) (*sdksns.UnsubscribeOutput, error) {
	f.unsubscribed = append(f.unsubscribed, awsSDK.ToString(params.SubscriptionArn))
	return &sdksns.UnsubscribeOutput{}, nil
}

type fakeAPIError struct {
	code string
	msg  string
}

func (e fakeAPIError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return e.code
}
func (e fakeAPIError) ErrorCode() string             { return e.code }
func (e fakeAPIError) ErrorMessage() string          { return e.msg }
func (e fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }
