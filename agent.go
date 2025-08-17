package go_agent

import (
	"context"
	"fmt"
	"log"
	"time"
)

type ParamsValue interface{}
type SharedData map[string]interface{}
type BaseNode struct {
	Params     map[string]ParamsValue
	Successors map[string]BaseNode
}

func (node *BaseNode) Init() {
	node.Params = make(map[string]ParamsValue)
	node.Successors = make(map[string]BaseNode)
}
func (node *BaseNode) SetParams(params map[string]ParamsValue) {
	node.Params = params
}
func (node *BaseNode) Next(nextNode BaseNode, action string) BaseNode {
	if action == "" {
		action = "default"
	}
	if node.Successors == nil {
		node.Successors = make(map[string]BaseNode)
	}
	if _, found := node.Successors[action]; found {
		log.Printf("WARNING: Overwriting successor for action '%s'", action)
	}
	node.Successors[action] = nextNode
	return nextNode
}

func (node *BaseNode) Prep(shared SharedData) any {
	return nil
}

func (node *BaseNode) Exec(prepRes any) any {
	return nil
}

func (node *BaseNode) Post(shared SharedData, prepRes any, execRes any) any {
	return nil
}

func (node *BaseNode) exec(prepRes any) any {
	return node.Exec(prepRes)
}
func (node *BaseNode) run(shared SharedData) any {
	p := node.Prep(shared)
	e := node.exec(p)
	return node.Post(shared, p, e)
}
func (node *BaseNode) Run(shared SharedData) any {
	if node.Successors != nil {
		log.Println("WARNING: Node won't run successors. Use Flow.")
	}
	return node.run(shared)
}

func (node *BaseNode) rshift(other BaseNode) BaseNode {
	return node.Next(other, "")
}

func (node *BaseNode) sub(action string) _ConditionalTransition {
	return _ConditionalTransition{source: *node, action: action}
}

type _ConditionalTransition struct {
	source BaseNode
	action string
}

func (T _ConditionalTransition) rshift(target BaseNode) any {
	return T.source.Next(target, T.action)

}

type Node struct {
	BaseNode
	maxRetries int
	wait       interface{} // Can be int or float64
	curRetry   int
}

func (n *Node) Init(maxRetries int, wait interface{}) {
	n.BaseNode.Init()
	n.maxRetries = maxRetries
	n.wait = wait
}

func (n Node) ExecFallback(prepRes any, err error) any {
	return err
}

func (n Node) exec(prepRes any) any {
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

		// If this was the last retry, call fallback
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
	return nil
}

type BatchNode struct {
	Node
}

func (n BatchNode) exec(prepRes any) any {
	// Implementation for batch processing
	items, ok := prepRes.([]any)
	if !ok || items == nil {
		items = []any{}
	}

	var results []any
	for _, item := range items {
		results = append(results, n.Node.exec(item))
	}
	return results
}

type Flow struct {
	BaseNode
	start BaseNode
}

func (f *Flow) Init(start BaseNode) {
	f.BaseNode.Init()
	f.start = start
}

func (f *Flow) Start(start BaseNode) BaseNode {
	f.start = start
	return f.start
}
func (f *Flow) GetNextNode(curr BaseNode, action string) BaseNode {
	if action == "" {
		action = "default"
	}
	next, found := curr.Successors[action]
	if !found && len(curr.Successors) > 0 {
		var actions []string
		for a := range curr.Successors {
			actions = append(actions, a)
		}
		log.Printf("WARNING: Flow ends: '%s' not found in %v", action, actions)
	}
	return next
}

func (f *Flow) orch(shared SharedData, params map[string]ParamsValue) any {
	// Initialize current node with a copy of the start node
	curr := f.start

	// Set parameters (use provided params or default to Flow's params)
	p := params
	if p == nil {
		p = f.Params
	}

	var lastAction any = nil

	// Loop until we reach a node that doesn't have a successor
	for curr.Successors != nil { // Check if current node is not empty
		// Set parameters on current node
		curr.SetParams(p)

		// Run the current node and get the action
		lastAction = curr.run(shared)

		// Get the next node based on the returned action
		next := f.GetNextNode(curr, fmt.Sprintf("%v", lastAction))

		// Update current node
		curr = next
	}

	return lastAction
}

func (f *Flow) run(shared SharedData) any {
	p := f.Prep(shared)
	o := f.orch(shared, nil)
	return f.Post(shared, p, o)
}

func (f *Flow) Post(shared SharedData, prepRes any, execRes any) any {
	return execRes
}

type BatchFlow struct {
	Flow
}

func (bf *BatchFlow) run(shared SharedData) any {
	pr := bf.Prep(shared)
	// If pr is nil, use an empty slice instead (equivalent to Python's "or []")
	if pr == nil {
		pr = []any{}
	}

	// Process each batch parameter
	batchParams, ok := pr.([]any)
	if !ok {
		// If pr is not a slice, make it a single-item slice
		batchParams = []any{pr}
	}

	var lastResult any
	for _, bp := range batchParams {
		// Convert bp to map if possible
		bpMap, ok := bp.(map[string]ParamsValue)
		if !ok {
			// If bp is not a map, continue with empty params
			bpMap = make(map[string]ParamsValue)
		}

		// Merge parameters (equivalent to Python's {**self.params, **bp})
		mergedParams := make(map[string]ParamsValue)
		// First copy bf.Params
		for k, v := range bf.Params {
			mergedParams[k] = v
		}
		// Then override with bp values
		for k, v := range bpMap {
			mergedParams[k] = v
		}

		// Call orch with merged parameters
		lastResult = bf.orch(shared, mergedParams)
	}

	// Return the post-processed result
	return bf.Post(shared, pr, lastResult)
}

// AsyncNodeInterface defines the interface for async nodes
type AsyncNodeInterface interface {
	PrepAsync(ctx context.Context, shared SharedData) (any, error)
	ExecAsync(ctx context.Context, prepRes any) (any, error)
	ExecFallbackAsync(ctx context.Context, prepRes any, err error) (any, error)
	PostAsync(ctx context.Context, shared SharedData, prepRes any, execRes any) (any, error)
	RunAsync(ctx context.Context, shared SharedData) (any, error)
}

// AsyncNode is the asynchronous version of Node
type AsyncNode struct {
	Node
	// Custom handlers for async operations
	prepAsyncFunc         func(ctx context.Context, shared SharedData) (any, error)
	execAsyncFunc         func(ctx context.Context, prepRes any) (any, error)
	execFallbackAsyncFunc func(ctx context.Context, prepRes any, err error) (any, error)
	postAsyncFunc         func(ctx context.Context, shared SharedData, prepRes any, execRes any) (any, error)
}

// NewAsyncNode creates a new AsyncNode with the given retry settings
func NewAsyncNode(maxRetries int, wait interface{}) *AsyncNode {
	node := &AsyncNode{}
	node.Node.Init(maxRetries, wait)
	return node
}

// SetPrepAsync sets the PrepAsync function
func (n *AsyncNode) SetPrepAsync(f func(ctx context.Context, shared SharedData) (any, error)) *AsyncNode {
	n.prepAsyncFunc = f
	return n
}

// SetExecAsync sets the ExecAsync function
func (n *AsyncNode) SetExecAsync(f func(ctx context.Context, prepRes any) (any, error)) *AsyncNode {
	n.execAsyncFunc = f
	return n
}

// SetExecFallbackAsync sets the ExecFallbackAsync function
func (n *AsyncNode) SetExecFallbackAsync(f func(ctx context.Context, prepRes any, err error) (any, error)) *AsyncNode {
	n.execFallbackAsyncFunc = f
	return n
}

// SetPostAsync sets the PostAsync function
func (n *AsyncNode) SetPostAsync(f func(ctx context.Context, shared SharedData, prepRes any, execRes any) (any, error)) *AsyncNode {
	n.postAsyncFunc = f
	return n
}

// PrepAsync is the asynchronous version of Prep
func (n *AsyncNode) PrepAsync(ctx context.Context, shared SharedData) (any, error) {
	if n.prepAsyncFunc != nil {
		return n.prepAsyncFunc(ctx, shared)
	}
	return n.Prep(shared), nil
}

// ExecAsync is the asynchronous version of Exec
func (n *AsyncNode) ExecAsync(ctx context.Context, prepRes any) (any, error) {
	if n.execAsyncFunc != nil {
		return n.execAsyncFunc(ctx, prepRes)
	}
	return n.Exec(prepRes), nil
}

// ExecFallbackAsync is the asynchronous version of ExecFallback
func (n *AsyncNode) ExecFallbackAsync(ctx context.Context, prepRes any, err error) (any, error) {
	if n.execFallbackAsyncFunc != nil {
		return n.execFallbackAsyncFunc(ctx, prepRes, err)
	}
	return n.ExecFallback(prepRes, err), nil
}

// PostAsync is the asynchronous version of Post
func (n *AsyncNode) PostAsync(ctx context.Context, shared SharedData, prepRes any, execRes any) (any, error) {
	if n.postAsyncFunc != nil {
		return n.postAsyncFunc(ctx, shared, prepRes, execRes)
	}
	return n.Post(shared, prepRes, execRes), nil
}

// execAsync is the asynchronous version of exec
func (n *AsyncNode) execAsync(ctx context.Context, prepRes any) (any, error) {
	for n.curRetry = 0; n.curRetry < n.maxRetries; n.curRetry++ {
		result, err := n.ExecAsync(ctx, prepRes)
		if err == nil {
			// If no error, return the result
			return result, nil
		}

		// If this was the last retry, call fallback
		if n.curRetry == n.maxRetries-1 {
			return n.ExecFallbackAsync(ctx, prepRes, err)
		}

		// Sleep before next retry if wait > 0
		switch w := n.wait.(type) {
		case int:
			if w > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(w) * time.Millisecond):
				}
			}
		case float64:
			if w > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(w) * time.Millisecond):
				}
			}
		}
	}

	// This should never be reached, but just in case
	return nil, fmt.Errorf("max retries exceeded")
}

// RunAsync is the asynchronous version of Run
func (n *AsyncNode) RunAsync(ctx context.Context, shared SharedData) (any, error) {
	if n.Successors != nil {
		log.Println("WARNING: Node won't run successors. Use AsyncFlow.")
	}
	return n.runAsync(ctx, shared)
}

// runAsync is the asynchronous version of run
func (n *AsyncNode) runAsync(ctx context.Context, shared SharedData) (any, error) {
	p, err := n.PrepAsync(ctx, shared)
	if err != nil {
		return nil, err
	}

	e, err := n.execAsync(ctx, p)
	if err != nil {
		return nil, err
	}

	return n.PostAsync(ctx, shared, p, e)
}

// Run overrides the synchronous Run method to prevent its use
func (n *AsyncNode) Run(shared SharedData) any {
	log.Fatal("Use RunAsync instead of Run for AsyncNode")
	return nil
}

// AsyncBatchNode is the asynchronous version of BatchNode
type AsyncBatchNode struct {
	AsyncNode
	BatchNode
}

// execAsync overrides AsyncNode's execAsync to handle batch processing
func (n AsyncBatchNode) execAsync(ctx context.Context, prepRes any) (any, error) {
	items, ok := prepRes.([]any)
	if !ok || items == nil {
		items = []any{}
	}

	var results []any
	for _, item := range items {
		result, err := n.AsyncNode.execAsync(ctx, item)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// AsyncParallelBatchNode processes items in parallel
type AsyncParallelBatchNode struct {
	AsyncNode
	BatchNode
}

// execAsync overrides AsyncNode's execAsync to handle parallel batch processing
func (n AsyncParallelBatchNode) execAsync(ctx context.Context, prepRes any) (any, error) {
	items, ok := prepRes.([]any)
	if !ok || items == nil {
		items = []any{}
	}

	results := make([]any, len(items))
	errCh := make(chan error, len(items))

	// Create a new context that will be canceled if any goroutine fails
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Process each item in parallel
	for i, item := range items {
		go func(index int, itemData any) {
			result, err := n.AsyncNode.execAsync(ctx, itemData)
			if err != nil {
				errCh <- err
				cancel() // Cancel other goroutines
				return
			}
			results[index] = result
			errCh <- nil
		}(i, item)
	}

	// Wait for all goroutines to complete
	for range items {
		if err := <-errCh; err != nil {
			return nil, err
		}
	}

	return results, nil
}

// AsyncFlow is the asynchronous version of Flow
type AsyncFlow struct {
	Flow
	AsyncNode
}

// orchAsync is the asynchronous version of orch
func (f AsyncFlow) orchAsync(ctx context.Context, shared SharedData, params map[string]ParamsValue) (any, error) {
	// Initialize current node with a copy of the start node
	curr := f.start

	// Set parameters (use provided params or default to Flow's params)
	p := params
	if p == nil {
		p = f.Params
	}

	var lastAction any = nil

	// Loop until we reach a node that doesn't have a successor
	for curr.Successors != nil {
		// Set parameters on current node
		curr.SetParams(p)

		// Run the current node and get the action
		// For simplicity, we'll just run the node synchronously
		// In a real implementation, you would need to check if the node is an async node
		// and call the appropriate method
		lastAction = curr.run(shared)

		// Get the next node based on the returned action
		next := f.GetNextNode(curr, fmt.Sprintf("%v", lastAction))

		// Update current node
		curr = next
	}

	return lastAction, nil
}

// runAsync is the asynchronous version of run
func (f AsyncFlow) runAsync(ctx context.Context, shared SharedData) (any, error) {
	p, err := f.PrepAsync(ctx, shared)
	if err != nil {
		return nil, err
	}

	o, err := f.orchAsync(ctx, shared, nil)
	if err != nil {
		return nil, err
	}

	return f.PostAsync(ctx, shared, p, o)
}

// PostAsync overrides AsyncNode's PostAsync
func (f AsyncFlow) PostAsync(ctx context.Context, shared SharedData, prepRes any, execRes any) (any, error) {
	return execRes, nil
}

// AsyncBatchFlow is the asynchronous version of BatchFlow
type AsyncBatchFlow struct {
	AsyncFlow
}

// runAsync overrides AsyncFlow's runAsync to handle batch processing
func (bf *AsyncBatchFlow) runAsync(ctx context.Context, shared SharedData) (any, error) {
	pr, err := bf.PrepAsync(ctx, shared)
	if err != nil {
		return nil, err
	}

	// If pr is nil, use an empty slice instead
	if pr == nil {
		pr = []any{}
	}

	// Process each batch parameter
	batchParams, ok := pr.([]any)
	if !ok {
		// If pr is not a slice, make it a single-item slice
		batchParams = []any{pr}
	}

	var lastResult any
	for _, bp := range batchParams {
		// Convert bp to map if possible
		bpMap, ok := bp.(map[string]ParamsValue)
		if !ok {
			// If bp is not a map, continue with empty params
			bpMap = make(map[string]ParamsValue)
		}

		// Merge parameters
		mergedParams := make(map[string]ParamsValue)
		// First copy bf.AsyncFlow.Params
		for k, v := range bf.AsyncFlow.Params {
			mergedParams[k] = v
		}
		// Then override with bp values
		for k, v := range bpMap {
			mergedParams[k] = v
		}

		// Call orchAsync with merged parameters
		var err error
		lastResult, err = bf.orchAsync(ctx, shared, mergedParams)
		if err != nil {
			return nil, err
		}
	}

	// Return the post-processed result
	return bf.PostAsync(ctx, shared, pr, lastResult)
}

// AsyncParallelBatchFlow processes batch items in parallel
type AsyncParallelBatchFlow struct {
	AsyncFlow
}

// runAsync overrides AsyncFlow's runAsync to handle parallel batch processing
func (bf *AsyncParallelBatchFlow) runAsync(ctx context.Context, shared SharedData) (any, error) {
	pr, err := bf.PrepAsync(ctx, shared)
	if err != nil {
		return nil, err
	}

	// If pr is nil, use an empty slice instead
	if pr == nil {
		pr = []any{}
	}

	// Process each batch parameter
	batchParams, ok := pr.([]any)
	if !ok {
		// If pr is not a slice, make it a single-item slice
		batchParams = []any{pr}
	}

	// Create a new context that will be canceled if any goroutine fails
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(batchParams))

	// Process each batch parameter in parallel
	for _, bp := range batchParams {
		go func(batchParam any) {
			// Convert bp to map if possible
			bpMap, ok := batchParam.(map[string]ParamsValue)
			if !ok {
				// If bp is not a map, continue with empty params
				bpMap = make(map[string]ParamsValue)
			}

			// Merge parameters
			mergedParams := make(map[string]ParamsValue)
			// First copy bf.AsyncFlow.Params
			for k, v := range bf.AsyncFlow.Params {
				mergedParams[k] = v
			}
			// Then override with bp values
			for k, v := range bpMap {
				mergedParams[k] = v
			}

			// Call orchAsync with merged parameters
			_, err := bf.orchAsync(ctx, shared, mergedParams)
			errCh <- err
		}(bp)
	}

	// Wait for all goroutines to complete
	for range batchParams {
		if err := <-errCh; err != nil {
			return nil, err
		}
	}

	// Return the post-processed result
	return bf.PostAsync(ctx, shared, pr, nil)
}
