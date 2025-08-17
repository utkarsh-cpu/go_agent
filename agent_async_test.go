package go_agent

import (
	"context"
	"fmt"
	"testing"
)

// TestAsyncNode tests the AsyncNode implementation
func TestAsyncNode(t *testing.T) {
	// Create a shared data map
	shared := make(SharedData)
	shared["input"] = "test"

	// Create an AsyncNode
	node := NewAsyncNode(3, 100) // 3 retries, 100ms wait
	t.Logf("Created AsyncNode with maxRetries=%d, wait=%v", node.maxRetries, node.wait)

	// Set the async functions
	node.SetPrepAsync(func(ctx context.Context, shared SharedData) (any, error) {
		t.Log("PrepAsync called")
		return shared["input"], nil
	})

	node.SetExecAsync(func(ctx context.Context, prepRes any) (any, error) {
		t.Logf("ExecAsync called with prepRes=%v", prepRes)
		return fmt.Sprintf("processed: %v", prepRes), nil
	})

	node.SetPostAsync(func(ctx context.Context, shared SharedData, prepRes any, execRes any) (any, error) {
		t.Logf("PostAsync called with prepRes=%v, execRes=%v", prepRes, execRes)
		shared["output"] = execRes
		return execRes, nil
	})

	// Run the node
	ctx := context.Background()
	t.Log("Calling RunAsync")
	result, err := node.RunAsync(ctx, shared)
	t.Logf("RunAsync returned result=%v, err=%v", result, err)
	if err != nil {
		t.Fatalf("RunAsync failed: %v", err)
	}

	// Check the result
	expected := "processed: test"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}

	// Check the shared data
	if shared["output"] != expected {
		t.Errorf("Expected shared[\"output\"] to be %q, got %q", expected, shared["output"])
	}
}

// TestAsyncBatchNode tests the AsyncBatchNode implementation
func TestAsyncBatchNode(t *testing.T) {
	t.Skip("AsyncBatchNode test not implemented yet")
}

// TestAsyncParallelBatchNode tests the AsyncParallelBatchNode implementation
func TestAsyncParallelBatchNode(t *testing.T) {
	t.Skip("AsyncParallelBatchNode test not implemented yet")
}

// TestAsyncFlow tests the AsyncFlow implementation
func TestAsyncFlow(t *testing.T) {
	t.Skip("AsyncFlow test not implemented yet")
}
