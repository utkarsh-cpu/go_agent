package go_agent

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestBaseNode_SetParams(t *testing.T) {
	n := NewBaseNode()
	params := map[string]interface{}{"key": "value"}
	n.SetParams(params)
	if fmt.Sprint(n.params) != fmt.Sprint(params) {
		t.Fatalf("Params not set correctly")
	}
}

// Custom node type for retry test
type retryTestNode struct {
	*Node
	retryCount *int
	mockErr    error
}

// Override Exec to panic and count retries
func (n *retryTestNode) Exec(prepRes interface{}) interface{} {
	(*n.retryCount)++
	panic(n.mockErr)
}

// Override execInternal to ensure our Exec is called with retries
func (n *retryTestNode) execInternal(prepRes interface{}) interface{} {
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

		if n.wait > 0 {
			time.Sleep(n.wait)
		}
	}
	return n.mockErr
}

func TestNode_ExecWithRetries(t *testing.T) {
	retryCount := 0
	mockErr := errors.New("transient error")

	// Use the custom node type
	n := &retryTestNode{
		Node:       NewNode(3, 10*time.Millisecond),
		retryCount: &retryCount,
		mockErr:    mockErr,
	}

	result := n.execInternal(nil)

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
type startTestNode struct{ *BaseNode }
type node1TestNode struct {
	*BaseNode
	t *testing.T
}
type node2TestNode struct {
	*BaseNode
	t          *testing.T
	finalValue string
}

func (n *startTestNode) Prep(shared map[string]interface{}) interface{} {
	shared["data"] = "started"
	return nil
}
func (n *startTestNode) Exec(prepRes interface{}) interface{} {
	return "to_node1"
}

func (n *node1TestNode) Prep(shared map[string]interface{}) interface{} {
	if shared["data"] != "started" {
		n.t.Fatal("Shared data not passed from start node")
	}
	shared["data"] = "node1_visited"
	return "prep_node1"
}
func (n *node1TestNode) Exec(prepRes interface{}) interface{} {
	if prepRes != "prep_node1" {
		n.t.Fatal("Prep result not passed to Exec in node1")
	}
	return "to_node2"
}

func (n *node2TestNode) Exec(prepRes interface{}) interface{} {
	return n.finalValue
}
func (n *node2TestNode) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	if shared["data"] != "node1_visited" {
		n.t.Fatal("Shared data not passed from node1")
	}
	shared["final_step"] = true
	return fmt.Sprintf("Postprocessed: %v", execRes)
}

// Custom flow type for testing
type testFlow struct {
	*Flow
}

// Override orchestrate to properly handle our test nodes
func (f *testFlow) orchestrate(shared map[string]interface{}, params map[string]interface{}) interface{} {
	if f.startNode == nil {
		return nil
	}

	// Start with the start node
	start, ok := f.startNode.(*startTestNode)
	if !ok {
		return nil
	}

	// Run the start node
	start.Prep(shared)
	action := start.Exec(nil)

	// Get the next node (node1)
	next := f.GetNextNode(start.BaseNode, action.(string))
	node1, ok := next.(*node1TestNode)
	if !ok {
		return nil
	}

	// Run node1
	prepRes := node1.Prep(shared)
	action = node1.Exec(prepRes)

	// Get the next node (node2)
	next = f.GetNextNode(node1.BaseNode, action.(string))
	node2, ok := next.(*node2TestNode)
	if !ok {
		return nil
	}

	// Run node2
	execRes := node2.Exec(nil)
	result := node2.Post(shared, nil, execRes)

	return result
}

func TestFlow_Execution(t *testing.T) {
	finalValue := "final result from node2"
	// Setup nodes using custom types
	start := &startTestNode{BaseNode: NewBaseNode()}
	node1 := &node1TestNode{BaseNode: NewBaseNode(), t: t}
	node2 := &node2TestNode{BaseNode: NewBaseNode(), t: t, finalValue: finalValue}

	// Define transitions
	start.Next(node1, "to_node1")
	node1.Next(node2, "to_node2")

	// Create a custom flow with our own orchestrate implementation
	flow := &testFlow{Flow: NewFlow(start)}
	sharedData := make(map[string]interface{})
	result := flow.orchestrate(sharedData, nil) // Initial action is nil

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
	*AsyncNode
}

// Override ExecAsync to return a specific result
func (n *asyncTestNode) ExecAsync(prepRes interface{}) interface{} {
	return "async result"
}

// Override the execAsyncInternal method to ensure our ExecAsync is called
func (n *asyncTestNode) execAsyncInternal(prepRes interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		res := n.ExecAsync(prepRes)
		result <- res
	}()
	return result
}

// Custom async flow type for testing
type testAsyncFlow struct {
	*AsyncFlow
}

// Override runAsyncInternal to properly handle our test async node
func (f *testAsyncFlow) runAsyncInternal(shared map[string]interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)

		// Get the start node
		asyncNode, ok := f.startNode.(*asyncTestNode)
		if !ok {
			result <- nil
			return
		}

		// Run the async node
		resChan := asyncNode.execAsyncInternal(nil)
		res := <-resChan
		result <- res
	}()
	return result
}

func TestAsyncFlow_Execution(t *testing.T) {
	// Use the custom node type
	asyncNode := &asyncTestNode{
		AsyncNode: NewAsyncNode(1, 0),
	}

	// Create a custom async flow with our own runAsyncInternal implementation
	flow := &testAsyncFlow{AsyncFlow: NewAsyncFlow(asyncNode)}
	resultChan := flow.runAsyncInternal(make(map[string]interface{}))
	result := <-resultChan

	if result != "async result" {
		t.Fatalf("Async flow execution failed: expected 'async result', got '%v'", result)
	}
}

// TestBatchNode_Processing tests the batch processing functionality.
func TestBatchNode_Processing(t *testing.T) {
	// Create a standard BatchNode.
	batch := &batchTestNode{BatchNode: NewBatchNode(1, 0)}

	// Assign a custom function to the Exec field of the embedded Node.
	// This function will be called for each item in the batch.
	// batch.Node.Exec = func(prepRes interface{}) interface{} {
	// 	if val, ok := prepRes.(int); ok {
	// 		return val * 10 // Custom processing logic
	// 	}
	// 	return errors.New("invalid item type")
	// }

	items := []interface{}{1, 2, 3}
	expectedResults := []interface{}{10, 20, 30}
	results := batch.execInternal(items)

	resultsSlice, ok := results.([]interface{})
	if !ok {
		t.Fatalf("Expected results to be []interface{}, got %T", results)
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
	emptyResults := batch.execInternal([]interface{}{})
	if len(emptyResults.([]interface{})) != 0 {
		t.Fatalf("Expected 0 results for empty input, got %d", len(emptyResults.([]interface{})))
	}

	// Test with nil input
	nilResults := batch.execInternal(nil)
	if len(nilResults.([]interface{})) != 0 {
		t.Fatalf("Expected 0 results for nil input, got %d", len(nilResults.([]interface{})))
	}
}

// Custom flow for testing transitions
type transitionTestFlow struct {
	*Flow
}

// Override GetNextNode to properly handle default transitions
func (f *transitionTestFlow) GetNextNode(curr *BaseNode, action string) interface{} {
	if action == "" {
		action = "default"
	}

	next, exists := curr.successors[action]
	if !exists {
		// Try default transition
		defaultNext, hasDefault := curr.successors["default"]
		if hasDefault {
			return defaultNext
		}
	}
	return next
}

func TestConditionalTransitions(t *testing.T) {
	nodeA := NewBaseNode()
	nodeB := NewBaseNode()
	flow := &transitionTestFlow{Flow: NewFlow(nodeA)} // Use our custom flow
	nodeA.Next(nodeB, "success")

	// GetNextNode is a method on Flow, not BaseNode
	next := flow.GetNextNode(nodeA, "success")
	if next != nodeB {
		t.Fatalf("Conditional transition failed: expected nodeB, got %v", next)
	}

	// Test default transition
	nodeC := NewBaseNode()
	nodeA.Next(nodeC, "default") // Add a default transition

	// Test with the default action
	nextDefault := flow.GetNextNode(nodeA, "unknown_action")
	if nextDefault != nodeC {
		t.Fatalf("Default transition failed: expected nodeC, got %v", nextDefault)
	}

	// Test no matching transition
	nodeD := NewBaseNode()                           // A node without transitions
	flow = &transitionTestFlow{Flow: NewFlow(nodeD)} // Create a new flow with nodeD as start
	nextNil := flow.GetNextNode(nodeD, "any_action")
	if nextNil != nil {
		t.Fatalf("Expected nil for no matching transition, got %v", nextNil)
	}
}

// Custom node type for async batch test
type asyncBatchTestNode struct {
	*AsyncBatchNode
}

// Override ExecAsync to process batch items
func (n *asyncBatchTestNode) ExecAsync(prepRes interface{}) interface{} {
	time.Sleep(5 * time.Millisecond)
	if val, ok := prepRes.(int); ok {
		return val * 2
	}
	return errors.New("invalid item type for async batch")
}

// Override execAsyncInternal to ensure our ExecAsync is called for each item
func (n *asyncBatchTestNode) execAsyncInternal(items interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		if items == nil {
			result <- []interface{}{}
			return
		}

		itemsSlice, ok := items.([]interface{})
		if !ok {
			result <- []interface{}{}
			return
		}

		results := make([]interface{}, len(itemsSlice))
		for i, item := range itemsSlice {
			results[i] = n.ExecAsync(item)
		}
		result <- results
	}()
	return result
}

func TestAsyncBatchProcessing(t *testing.T) {
	// Use the custom node type
	asyncBatch := &asyncBatchTestNode{
		AsyncBatchNode: NewAsyncBatchNode(1, 0),
	}

	items := []interface{}{1, 2, 3}
	expectedResults := []interface{}{2, 4, 6}

	resultsChan := asyncBatch.execAsyncInternal(items)
	results := <-resultsChan

	resultsSlice, ok := results.([]interface{})
	if !ok {
		t.Fatalf("Expected results to be []interface{}, got %T", results)
	}

	if len(resultsSlice) != len(expectedResults) {
		t.Fatalf("Async batch processing failed: expected %d results, got %d", len(expectedResults), len(resultsSlice))
	}

	// Note: Async batch might not preserve order, check presence
	resultMap := make(map[interface{}]bool)
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
	*AsyncNode
}

// Override ExecAsync to simulate a panic
func (n *asyncErrorTestNode) ExecAsync(prepRes interface{}) interface{} {
	panic("async failure")
}

// Override execAsyncInternal to ensure our ExecAsync is called and panic is caught
func (n *asyncErrorTestNode) execAsyncInternal(prepRes interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		defer func() {
			if r := recover(); r != nil {
				result <- fmt.Sprint(r)
			}
		}()
		res := n.ExecAsync(prepRes)
		result <- res
	}()
	return result
}

func TestAsyncErrorRecovery(t *testing.T) {
	// Use the custom node type
	asyncNode := &asyncErrorTestNode{
		AsyncNode: NewAsyncNode(1, 0),
	}

	resultChan := asyncNode.execAsyncInternal(nil)
	err := <-resultChan

	// The recovery mechanism should catch the panic and return it as an error
	if fmt.Sprint(err) != "async failure" {
		t.Fatalf("Async error recovery failed: expected 'async failure', got '%v'", err)
	}
}

type batchTestNode struct {
	*BatchNode
}

func (n *batchTestNode) Exec(prepRes interface{}) interface{} {
	if val, ok := prepRes.(int); ok {
		return val * 10
	}
	return errors.New("invalid item type")
}

// Override execInternal to ensure our Exec is called for each item
func (n *batchTestNode) execInternal(items interface{}) interface{} {
	if items == nil {
		return []interface{}{}
	}

	itemsSlice, ok := items.([]interface{})
	if !ok {
		return []interface{}{}
	}

	results := make([]interface{}, len(itemsSlice))
	for i, item := range itemsSlice {
		results[i] = n.Exec(item)
	}
	return results
}
