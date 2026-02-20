package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"h2/internal/session/agent/monitor"
)

// TestIntegration_FullPipeline verifies the end-to-end Codex adapter flow:
//
//	CodexAdapter.PrepareForLaunch → OtelServer (random port)
//	mockCodex sends OTEL traces via HTTP → OtelParser → events → AgentMonitor
//
// Verifies: session ID discovery, token counting, tool tracking, state transitions.
func TestIntegration_FullPipeline(t *testing.T) {
	// 1. Create adapter and monitor, wire them together.
	a := New(nil)
	mon := monitor.New()

	cfg, err := a.PrepareForLaunch("test-codex", "")
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer a.Stop()

	// Verify we got -c flag with OTEL endpoint.
	if len(cfg.PrependArgs) != 2 || cfg.PrependArgs[0] != "-c" {
		t.Fatalf("PrependArgs = %v, want [-c, ...]", cfg.PrependArgs)
	}

	port := a.OtelPort()
	if port == 0 {
		t.Fatal("OtelPort should be non-zero after PrepareForLaunch")
	}

	// 2. Start adapter and monitor.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The adapter forwards events from its internal channel to external.
	// The monitor consumes from external.
	externalEvents := make(chan monitor.AgentEvent, 256)
	go a.Start(ctx, externalEvents)
	go mon.Run(ctx)

	// Bridge: forward external events to the monitor.
	go func() {
		for {
			select {
			case ev := <-externalEvents:
				mon.Events() <- ev
			case <-ctx.Done():
				return
			}
		}
	}()

	// Give goroutines time to start.
	time.Sleep(20 * time.Millisecond)

	// 3. Simulate Codex sending OTEL trace events.
	tracesURL := fmt.Sprintf("http://127.0.0.1:%d/v1/traces", port)

	// 3a. conversation_starts → session ID discovery
	postTrace(t, tracesURL, "codex.conversation_starts", []otelAttribute{
		{Key: "conversation.id", Value: otelAttrValue{StringValue: "conv-integration-1"}},
		{Key: "model", Value: otelAttrValue{StringValue: "o3-mini"}},
	})

	// 3b. user_prompt → turn started
	postTrace(t, tracesURL, "codex.user_prompt", []otelAttribute{
		{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("25")}},
	})

	// 3c. tool_result → tool tracking
	postTrace(t, tracesURL, "codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-1"}},
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("250")}},
		{Key: "success", Value: otelAttrValue{StringValue: "true"}},
	})

	// 3d. sse_event (response.completed) → token counting
	postTrace(t, tracesURL, "codex.sse_event", []otelAttribute{
		{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
		{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("1000")}},
		{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("500")}},
		{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("200")}},
	})

	// 3e. tool_decision (ask_user) → approval requested
	postTrace(t, tracesURL, "codex.tool_decision", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-2"}},
		{Key: "decision", Value: otelAttrValue{StringValue: "ask_user"}},
	})

	// 3f. Second tool_result → accumulation
	postTrace(t, tracesURL, "codex.tool_result", []otelAttribute{
		{Key: "tool_name", Value: otelAttrValue{StringValue: "shell"}},
		{Key: "call_id", Value: otelAttrValue{StringValue: "call-2"}},
		{Key: "duration_ms", Value: otelAttrValue{IntValue: json.RawMessage("100")}},
		{Key: "success", Value: otelAttrValue{StringValue: "true"}},
	})

	// 4. Wait for events to propagate through the pipeline.
	// The monitor processes events asynchronously, so we poll.
	waitFor(t, 2*time.Second, "session ID", func() bool {
		return mon.ThreadID() == "conv-integration-1"
	})

	waitFor(t, 2*time.Second, "model", func() bool {
		return mon.Model() == "o3-mini"
	})

	waitFor(t, 2*time.Second, "token counts", func() bool {
		m := mon.Metrics()
		return m.InputTokens == 1000 && m.OutputTokens == 500 && m.CachedTokens == 200
	})

	waitFor(t, 2*time.Second, "turn count", func() bool {
		return mon.Metrics().TurnCount == 1
	})

	waitFor(t, 2*time.Second, "tool counts", func() bool {
		m := mon.Metrics()
		return m.ToolCounts["shell"] == 2
	})

	// 5. Verify HandleHookEvent returns false (Codex doesn't use hooks).
	if a.HandleHookEvent("PreToolUse", nil) {
		t.Error("HandleHookEvent should return false for Codex")
	}
}

// TestIntegration_MultipleTracePayloads verifies that the adapter handles
// multiple spans in a single /v1/traces POST (batch delivery).
func TestIntegration_MultipleTracePayloads(t *testing.T) {
	a := New(nil)
	mon := monitor.New()

	_, err := a.PrepareForLaunch("test-codex", "")
	if err != nil {
		t.Fatalf("PrepareForLaunch: %v", err)
	}
	defer a.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	externalEvents := make(chan monitor.AgentEvent, 256)
	go a.Start(ctx, externalEvents)
	go mon.Run(ctx)
	go func() {
		for {
			select {
			case ev := <-externalEvents:
				mon.Events() <- ev
			case <-ctx.Done():
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)

	// Send multiple spans in a single POST (batch).
	tracesURL := fmt.Sprintf("http://127.0.0.1:%d/v1/traces", a.OtelPort())
	payload := otelTracesPayload{
		ResourceSpans: []otelResourceSpans{{
			ScopeSpans: []otelScopeSpans{{
				Spans: []otelSpan{
					{
						Name: "codex.conversation_starts",
						Attributes: []otelAttribute{
							{Key: "conversation.id", Value: otelAttrValue{StringValue: "batch-conv"}},
							{Key: "model", Value: otelAttrValue{StringValue: "o3"}},
						},
					},
					{
						Name: "codex.user_prompt",
						Attributes: []otelAttribute{
							{Key: "prompt_length", Value: otelAttrValue{IntValue: json.RawMessage("10")}},
						},
					},
					{
						Name: "codex.sse_event",
						Attributes: []otelAttribute{
							{Key: "event.kind", Value: otelAttrValue{StringValue: "response.completed"}},
							{Key: "input_token_count", Value: otelAttrValue{IntValue: json.RawMessage("800")}},
							{Key: "output_token_count", Value: otelAttrValue{IntValue: json.RawMessage("400")}},
							{Key: "cached_token_count", Value: otelAttrValue{IntValue: json.RawMessage("0")}},
						},
					},
				},
			}},
		}},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(tracesURL, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST /v1/traces: %v", err)
	}
	resp.Body.Close()

	waitFor(t, 2*time.Second, "batch session ID", func() bool {
		return mon.ThreadID() == "batch-conv"
	})
	waitFor(t, 2*time.Second, "batch tokens", func() bool {
		m := mon.Metrics()
		return m.InputTokens == 800 && m.OutputTokens == 400
	})
	waitFor(t, 2*time.Second, "batch turn count", func() bool {
		return mon.Metrics().TurnCount == 1
	})
}

// --- Test helpers ---

// postTrace sends a single-span OTEL trace payload to the given URL.
func postTrace(t *testing.T, url, spanName string, attrs []otelAttribute) {
	t.Helper()
	body := makeTracePayload(spanName, attrs)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST %s: %v", spanName, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", spanName, resp.StatusCode)
	}
}

// waitFor polls a condition with a deadline, failing the test if not met.
func waitFor(t *testing.T, timeout time.Duration, desc string, condition func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", desc)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
