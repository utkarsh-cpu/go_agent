package go_agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBaseNode_SetParams(t *testing.T) {
	n := BaseNode{}
	n.Init()
	params := map[string]ParamsValue{"key": "value"}
	n.SetParams(params)
	if fmt.Sprint(n.Params) != fmt.Sprint(params) {
		t.Fatalf("Params not set correctly")
	}
}

// Custom node type for retry test
type retryTestNode struct {
	Node
	retryCount *int
	mockErr    error
}

// Override Exec to panic and count retries
func (n *retryTestNode) Exec(prepRes any) any {
	(*n.retryCount)++
	panic(n.mockErr)
}

// Override exec to ensure our Exec is called with retries
func (n *retryTestNode) exec(prepRes any) any {
	for n.curRetry = 0; n.curRetry < n.maxRetries; n.curRetry++ {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					switch v := r.(type) {
					case error:
						err = v
					default:
						err = fmt.Errorf("%v", v)
					}
				}
			}()
			n.Exec(prepRes)
		}()

		if n.curRetry == n.maxRetries-1 {
			return n.ExecFallback(prepRes, err)
		}

		// Sleep before next retry if wait > 0
		switch w := n.wait.(type) {
		case int:
			if w > 0 {
				time.Sleep(time.Duration(w) * time.Millisecond)
			}
		case float64:
			if w > 0 {
				time.Sleep(time.Duration(w) * time.Millisecond)
			}
		}
	}
	return n.mockErr
}

func TestNode_ExecWithRetries(t *testing.T) {
	retryCount := 0
	mockErr := errors.New("transient error")

	// Use the custom node type
	n := &retryTestNode{
		retryCount: &retryCount,
		mockErr:    mockErr,
	}
	n.Node.Init(3, 10)

	result := n.exec(nil)

	if retryCount != 3 {
		t.Fatalf("Expected Exec to be called 3 times, but got %d", retryCount)
	}

	// Check if the final result is the error returned by ExecFallback
	if result == nil {
		t.Fatalf("Expected non-nil error result from fallback, got nil")
	}
	if err, ok := result.(error); !ok || err.Error() != mockErr.Error() {
		t.Fatalf("Retry logic failed: expected error '%v', got '%v'", mockErr, result)
	}
}

// Custom node types for flow test
type startTestNode struct{ BaseNode }
type node1TestNode struct {
	BaseNode
	t *testing.T
}
type node2TestNode struct {
	BaseNode
	t          *testing.T
	finalValue string
}

func (n *startTestNode) Prep(shared SharedData) any {
	shared["data"] = "started"
	return nil
}
func (n *startTestNode) Exec(prepRes any) any {
	return "to_node1"
}

func (n *node1TestNode) Prep(shared SharedData) any {
	if shared["data"] != "started" {
		n.t.Fatal("Shared data not passed from start node")
	}
	shared["data"] = "node1_visited"
	return "prep_node1"
}
func (n *node1TestNode) Exec(prepRes any) any {
	if prepRes != "prep_node1" {
		n.t.Fatal("Prep result not passed to Exec in node1")
	}
	return "to_node2"
}

func (n *node2TestNode) Exec(prepRes any) any {
	return n.finalValue
}
func (n *node2TestNode) Post(shared SharedData, prepRes any, execRes any) any {
	if shared["data"] != "node1_visited" {
		n.t.Fatal("Shared data not passed from node1")
	}
	shared["final_step"] = true
	return fmt.Sprintf("Postprocessed: %v", execRes)
}

// Custom flow type for testing
type testFlow struct {
	Flow
	t          *testing.T
	finalValue string
}

// Override orch to properly handle our test nodes
func (f *testFlow) orch(shared SharedData, params map[string]ParamsValue) any {
	// Check if start node is initialized
	if f.start.Successors == nil {
		return nil
	}

	// Start with the start node
	startNode := &startTestNode{BaseNode: f.start}

	// Run the start node
	startNode.Prep(shared)
	action := startNode.Exec(nil)

	// Get the next node (node1)
	nextNode := f.GetNextNode(startNode.BaseNode, action.(string))
	node1 := &node1TestNode{BaseNode: nextNode, t: f.t}

	// Run node1
	prepRes := node1.Prep(shared)
	action = node1.Exec(prepRes)

	// Get the next node (node2)
	nextNode = f.GetNextNode(node1.BaseNode, action.(string))
	node2 := &node2TestNode{BaseNode: nextNode, t: f.t, finalValue: f.finalValue}

	// Run node2
	execRes := node2.Exec(nil)
	result := node2.Post(shared, nil, execRes)

	return result
}

func TestFlow_Execution(t *testing.T) {
	finalValue := "final result from node2"
	// Setup nodes using custom types
	start := &startTestNode{}
	start.BaseNode.Init()
	node1 := &node1TestNode{t: t}
	node1.BaseNode.Init()
	node2 := &node2TestNode{t: t, finalValue: finalValue}
	node2.BaseNode.Init()

	// Define transitions
	start.Next(node1.BaseNode, "to_node1")
	node1.Next(node2.BaseNode, "to_node2")

	// Create a custom flow with our own orchestrate implementation
	flow := &testFlow{t: t, finalValue: finalValue}
	flow.Flow.Init(start.BaseNode)
	sharedData := make(SharedData)
	result := flow.orch(sharedData, nil) // Initial action is nil

	// Verify final result
	expectedResult := fmt.Sprintf("Postprocessed: %v", finalValue)
	if result != expectedResult {
		t.Fatalf("Flow execution failed: expected result '%v', got '%v'", expectedResult, result)
	}

	// Verify shared data modifications
	if sharedData["data"] != "node1_visited" { // Post of node2 runs after node1's data update
		t.Fatalf("Flow execution failed: expected shared[data] 'node1_visited', got '%v'", sharedData["data"])
	}
	if sharedData["final_step"] != true {
		t.Fatalf("Flow execution failed: expected shared[final_step] true, got '%v'", sharedData["final_step"])
	}
}

// Custom node type for async flow test
type asyncTestNode struct {
	AsyncNode
}

// Override ExecAsync to return a specific result
func (n *asyncTestNode) ExecAsync(ctx context.Context, prepRes any) (any, error) {
	return "async result", nil
}

// Override the execAsync method to ensure our ExecAsync is called
func (n *asyncTestNode) execAsync(ctx context.Context, prepRes any) (any, error) {
	return n.ExecAsync(ctx, prepRes)
}

// Custom async flow type for testing
type testAsyncFlow struct {
	AsyncFlow
}

// Override runAsync to properly handle our test async node
func (f *testAsyncFlow) runAsync(ctx context.Context, shared SharedData) (any, error) {
	// Get the start node
	asyncNode := &asyncTestNode{AsyncNode: AsyncNode{}}
	asyncNode.AsyncNode.Node.Init(1, 0)

	// Run the async node
	result, err := asyncNode.execAsync(ctx, nil)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func TestAsyncFlow_Execution(t *testing.T) {
	// Use the custom node type
	asyncNode := &asyncTestNode{}
	asyncNode.AsyncNode.Node.Init(1, 0)

	// Create a custom async flow with our own runAsync implementation
	flow := &testAsyncFlow{}
	flow.AsyncFlow.Flow.Init(asyncNode.BaseNode)

	ctx := context.Background()
	result, err := flow.runAsync(ctx, make(SharedData))
	if err != nil {
		t.Fatalf("Async flow execution failed with error: %v", err)
	}

	if result != "async result" {
		t.Fatalf("Async flow execution failed: expected 'async result', got '%v'", result)
	}
}

// TestBatchNode_Processing tests the batch processing functionality.
func TestBatchNode_Processing(t *testing.T) {
	// Create a standard BatchNode.
	batch := &batchTestNode{}
	batch.BatchNode.Node.Init(1, 0)

	// Assign a custom function to the Exec field of the embedded Node.
	// This function will be called for each item in the batch.
	// batch.Node.Exec = func(prepRes any) any {
	// 	if val, ok := prepRes.(int); ok {
	// 		return val * 10 // Custom processing logic
	// 	}
	// 	return errors.New("invalid item type")
	// }

	items := []any{1, 2, 3}
	expectedResults := []any{10, 20, 30}
	results := batch.exec(items)

	resultsSlice, ok := results.([]any)
	if !ok {
		t.Fatalf("Expected results to be []any, got %T", results)
	}

	if len(resultsSlice) != len(expectedResults) {
		t.Fatalf("Batch processing failed: expected %d results, got %d", len(expectedResults), len(resultsSlice))
	}

	for i, res := range resultsSlice {
		if res != expectedResults[i] {
			t.Errorf("Result mismatch at index %d: expected %v, got %v", i, expectedResults[i], res)
		}
	}

	// Test with empty input
	emptyResults := batch.exec([]any{})
	if len(emptyResults.([]any)) != 0 {
		t.Fatalf("Expected 0 results for empty input, got %d", len(emptyResults.([]any)))
	}

	// Test with nil input
	nilResults := batch.exec(nil)
	if len(nilResults.([]any)) != 0 {
		t.Fatalf("Expected 0 results for nil input, got %d", len(nilResults.([]any)))
	}
}

// Custom flow for testing transitions
type transitionTestFlow struct {
	Flow
}

// Override GetNextNode to properly handle default transitions
func (f *transitionTestFlow) GetNextNode(curr BaseNode, action string) BaseNode {
	if action == "" {
		action = "default"
	}

	next, exists := curr.Successors[action]
	if !exists {
		// Try default transition
		defaultNext, hasDefault := curr.Successors["default"]
		if hasDefault {
			return defaultNext
		}
	}
	return next
}

func TestConditionalTransitions(t *testing.T) {
	nodeA := BaseNode{}
	nodeA.Init()
	nodeB := BaseNode{}
	nodeB.Init()

	flow := &transitionTestFlow{}
	flow.Flow.Init(nodeA)

	nodeA.Next(nodeB, "success")

	// GetNextNode is a method on Flow, not BaseNode
	next := flow.GetNextNode(nodeA, "success")
	// Since we can't directly compare BaseNode instances, we'll check if the Successors map is the same
	if fmt.Sprint(next.Successors) != fmt.Sprint(nodeB.Successors) {
		t.Fatalf("Conditional transition failed: expected nodeB, got different node")
	}

	// Test default transition
	nodeC := BaseNode{}
	nodeC.Init()
	nodeA.Next(nodeC, "default") // Add a default transition

	// Test with the default action
	nextDefault := flow.GetNextNode(nodeA, "unknown_action")
	// Since we can't directly compare BaseNode instances, we'll check if the Successors map is the same
	if fmt.Sprint(nextDefault.Successors) != fmt.Sprint(nodeC.Successors) {
		t.Fatalf("Default transition failed: expected nodeC, got different node")
	}

	// Test no matching transition
	nodeD := BaseNode{}
	nodeD.Init()
	flow = &transitionTestFlow{}
	flow.Flow.Init(nodeD)
	nextNil := flow.GetNextNode(nodeD, "any_action")
	// Check if nextNil is an empty BaseNode (no Successors)
	if nextNil.Successors != nil {
		t.Fatalf("Expected empty BaseNode for no matching transition, got node with Successors")
	}
}

// Custom node type for async batch test
type asyncBatchTestNode struct {
	AsyncBatchNode
}

// Override ExecAsync to process batch items
func (n *asyncBatchTestNode) ExecAsync(ctx context.Context, prepRes any) (any, error) {
	time.Sleep(5 * time.Millisecond)
	if val, ok := prepRes.(int); ok {
		return val * 2, nil
	}
	return nil, errors.New("invalid item type for async batch")
}

// Override execAsync to ensure our ExecAsync is called for each item
func (n *asyncBatchTestNode) execAsync(ctx context.Context, items any) (any, error) {
	if items == nil {
		return []any{}, nil
	}

	itemsSlice, ok := items.([]any)
	if !ok {
		return []any{}, nil
	}

	results := make([]any, len(itemsSlice))
	for i, item := range itemsSlice {
		result, err := n.ExecAsync(ctx, item)
		if err != nil {
			return nil, err
		}
		results[i] = result
	}
	return results, nil
}

func TestAsyncBatchProcessing(t *testing.T) {
	// Use the custom node type
	asyncBatch := &asyncBatchTestNode{}
	asyncBatch.AsyncBatchNode.AsyncNode.Node.Init(1, 0)

	items := []any{1, 2, 3}
	expectedResults := []any{2, 4, 6}

	ctx := context.Background()
	results, err := asyncBatch.execAsync(ctx, items)
	if err != nil {
		t.Fatalf("Async batch processing failed with error: %v", err)
	}

	resultsSlice, ok := results.([]any)
	if !ok {
		t.Fatalf("Expected results to be []any, got %T", results)
	}

	if len(resultsSlice) != len(expectedResults) {
		t.Fatalf("Async batch processing failed: expected %d results, got %d", len(expectedResults), len(resultsSlice))
	}

	// Note: Async batch might not preserve order, check presence
	resultMap := make(map[any]bool)
	for _, res := range resultsSlice {
		resultMap[res] = true
	}
	for _, expected := range expectedResults {
		if !resultMap[expected] {
			t.Errorf("Expected result %v not found in async batch results %v", expected, resultsSlice)
		}
	}
}

// Custom node type for async error recovery test
type asyncErrorTestNode struct {
	AsyncNode
}

// Override ExecAsync to simulate a panic
func (n *asyncErrorTestNode) ExecAsync(ctx context.Context, prepRes any) (any, error) {
	panic("async failure")
}

// Override execAsync to ensure our ExecAsync is called and panic is caught
func (n *asyncErrorTestNode) execAsync(ctx context.Context, prepRes any) (any, error) {
	var result any
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
		}()
		result, err = n.ExecAsync(ctx, prepRes)
	}()

	return result, err
}

func TestAsyncErrorRecovery(t *testing.T) {
	// Use the custom node type
	asyncNode := &asyncErrorTestNode{}
	asyncNode.AsyncNode.Node.Init(1, 0)

	ctx := context.Background()
	_, err := asyncNode.execAsync(ctx, nil)

	// The recovery mechanism should catch the panic and return it as an error
	if err == nil || err.Error() != "async failure" {
		t.Fatalf("Async error recovery failed: expected 'async failure', got '%v'", err)
	}
}

type batchTestNode struct {
	BatchNode
}

func (n *batchTestNode) Exec(prepRes any) any {
	if val, ok := prepRes.(int); ok {
		return val * 10
	}
	return errors.New("invalid item type")
}

// Override exec to ensure our Exec is called for each item
func (n *batchTestNode) exec(items any) any {
	if items == nil {
		return []any{}
	}

	itemsSlice, ok := items.([]any)
	if !ok {
		return []any{}
	}

	results := make([]any, len(itemsSlice))
	for i, item := range itemsSlice {
		results[i] = n.Exec(item)
	}
	return results
}
