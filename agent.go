package go_agent

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// BaseNode represents the basic node structure in the agent framework
type BaseNode struct {
	params     map[string]interface{}
	successors map[string]interface{}
}

// NewBaseNode creates a new BaseNode instance
func NewBaseNode() *BaseNode {
	return &BaseNode{
		params:     make(map[string]interface{}),
		successors: make(map[string]interface{}),
	}
}

// SetParams sets the parameters for the node
func (b *BaseNode) SetParams(params map[string]interface{}) {
	b.params = params
}

// Next adds a successor node for a specific action
func (b *BaseNode) Next(node interface{}, action string) interface{} {
	if action == "" {
		action = "default"
	}
	if _, exists := b.successors[action]; exists {
		log.Printf("Warning: Overwriting successor for action '%s'", action)
	}
	b.successors[action] = node
	return node
}

// Prep prepares the node for execution
func (b *BaseNode) Prep(shared map[string]interface{}) interface{} {
	return nil
}

// Exec executes the node's main functionality
func (b *BaseNode) Exec(prepRes interface{}) interface{} {
	return nil
}

// Post processes the results after execution
func (b *BaseNode) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	return nil
}

// execInternal executes the node internally
func (b *BaseNode) execInternal(prepRes interface{}) interface{} {
	return b.Exec(prepRes)
}

// Run executes the node's full lifecycle
func (b *BaseNode) Run(shared map[string]interface{}) interface{} {
	if len(b.successors) > 0 {
		log.Println("Warning: Node won't run successors. Use Flow.")
	}
	return b.runInternal(shared)
}

// runInternal runs the node's internal execution flow
func (b *BaseNode) runInternal(shared map[string]interface{}) interface{} {
	prepRes := b.Prep(shared)
	execRes := b.execInternal(prepRes)
	return b.Post(shared, prepRes, execRes)
}

// ConditionalTransition represents a transition with a specific action
type conditionalTransition struct {
	src    *BaseNode
	action string
}

// NewConditionalTransition creates a new conditional transition
func newConditionalTransition(src *BaseNode, action string) *conditionalTransition {
	return &conditionalTransition{
		src:    src,
		action: action,
	}
}

// Next connects the transition to a target node
func (c *conditionalTransition) Next(target interface{}) interface{} {
	return c.src.Next(target, c.action)
}

// Node extends BaseNode with retry capabilities
type Node struct {
	*BaseNode
	maxRetries int
	wait       time.Duration
	curRetry   int
}

// NewNode creates a new Node instance
func NewNode(maxRetries int, wait time.Duration) *Node {
	return &Node{
		BaseNode:   NewBaseNode(),
		maxRetries: maxRetries,
		wait:       wait,
	}
}

// ExecFallback handles execution failures
func (n *Node) ExecFallback(prepRes interface{}, err error) interface{} {
	return err
}

// ExecInternal implements retry logic for execution
func (n *Node) execInternal(prepRes interface{}) interface{} {
	for n.curRetry = 0; n.curRetry < n.maxRetries; n.curRetry++ {
		var err error
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

		result := n.Exec(prepRes)
		if err == nil {
			return result
		}

		if n.curRetry == n.maxRetries-1 {
			return n.ExecFallback(prepRes, err)
		}

		if n.wait > 0 {
			time.Sleep(n.wait)
		}
	}
	return nil
}

// BatchNode processes items in batches
type BatchNode struct {
	*Node
}

// NewBatchNode creates a new BatchNode instance
func NewBatchNode(maxRetries int, wait time.Duration) *BatchNode {
	return &BatchNode{
		Node: NewNode(maxRetries, wait),
	}
}

// ExecInternal processes each item in the batch
func (b *BatchNode) execInternal(items interface{}) interface{} {
	if items == nil {
		return []interface{}{}
	}

	itemsSlice, ok := items.([]interface{})
	if !ok {
		return []interface{}{}
	}

	results := make([]interface{}, len(itemsSlice))
	for i, item := range itemsSlice {
		results[i] = b.Node.execInternal(item)
	}
	return results
}

// Flow orchestrates the execution of multiple nodes
type Flow struct {
	*BaseNode
	startNode interface{}
}

// NewFlow creates a new Flow instance
func NewFlow(start interface{}) *Flow {
	return &Flow{
		BaseNode:  NewBaseNode(),
		startNode: start,
	}
}

// Start sets the starting node for the flow
func (f *Flow) Start(start interface{}) interface{} {
	f.startNode = start
	return start
}

// GetNextNode determines the next node based on the current node and action
func (f *Flow) GetNextNode(curr *BaseNode, action string) interface{} {
	if action == "" {
		action = "default"
	}

	next, exists := curr.successors[action]
	if !exists && len(curr.successors) > 0 {
		var actions []string
		for k := range curr.successors {
			actions = append(actions, k)
		}
		log.Printf("Warning: Flow ends: '%s' not found in %v", action, actions)
	}
	return next
}

// orchestrate manages the flow of execution through nodes
func (f *Flow) orchestrate(shared map[string]interface{}, params map[string]interface{}) interface{} {
	if f.startNode == nil {
		return nil
	}

	if params == nil {
		params = make(map[string]interface{})
		for k, v := range f.params {
			params[k] = v
		}
	}

	// Deep copy of startNode would be implemented here
	// For simplicity, we're using the original node
	curr, ok := f.startNode.(*BaseNode)
	if !ok {
		return nil
	}

	var lastAction interface{}
	for curr != nil {
		curr.SetParams(params)
		lastAction = curr.runInternal(shared)

		nextNode := f.GetNextNode(curr, fmt.Sprintf("%v", lastAction))
		if nextNode == nil {
			break
		}

		nextBaseNode, ok := nextNode.(*BaseNode)
		if !ok {
			break
		}
		curr = nextBaseNode
	}

	return lastAction
}

// runInternal executes the flow
func (f *Flow) runInternal(shared map[string]interface{}) interface{} {
	prepRes := f.Prep(shared)
	orchRes := f.orchestrate(shared, nil)
	return f.Post(shared, prepRes, orchRes)
}

// Post processes the results after flow execution
func (f *Flow) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	return execRes
}

// BatchFlow processes batches of inputs through a flow
type BatchFlow struct {
	*Flow
}

// NewBatchFlow creates a new BatchFlow instance
func NewBatchFlow(start interface{}) *BatchFlow {
	return &BatchFlow{
		Flow: NewFlow(start),
	}
}

// runInternal processes each batch item through the flow
func (b *BatchFlow) runInternal(shared map[string]interface{}) interface{} {
	prepRes := b.Prep(shared)
	prepSlice, ok := prepRes.([]interface{})
	if !ok || prepSlice == nil {
		prepSlice = []interface{}{}
	}

	for _, bp := range prepSlice {
		bpMap, ok := bp.(map[string]interface{})
		if !ok {
			continue
		}

		params := make(map[string]interface{})
		for k, v := range b.params {
			params[k] = v
		}
		for k, v := range bpMap {
			params[k] = v
		}

		b.orchestrate(shared, params)
	}

	return b.Post(shared, prepRes, nil)
}

// AsyncNode represents a node that can be executed asynchronously
type AsyncNode struct {
	*Node
}

// NewAsyncNode creates a new AsyncNode instance
func NewAsyncNode(maxRetries int, wait time.Duration) *AsyncNode {
	return &AsyncNode{
		Node: NewNode(maxRetries, wait),
	}
}

// PrepAsync prepares the node asynchronously
func (a *AsyncNode) PrepAsync(shared map[string]interface{}) interface{} {
	return nil
}

// ExecAsync executes the node asynchronously
func (a *AsyncNode) ExecAsync(prepRes interface{}) interface{} {
	return nil
}

// ExecFallbackAsync handles execution failures asynchronously
func (a *AsyncNode) ExecFallbackAsync(prepRes interface{}, err error) interface{} {
	return err
}

// PostAsync processes results asynchronously
func (a *AsyncNode) PostAsync(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	return nil
}

// RunAsync runs the node asynchronously
func (a *AsyncNode) RunAsync(shared map[string]interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		if len(a.successors) > 0 {
			log.Println("Warning: Node won't run successors. Use AsyncFlow.")
		}
		res := a.runAsyncInternal(shared)
		result <- <-res
	}()
	return result
}

// RunAsyncInternal runs the node's internal async execution flow
func (a *AsyncNode) runAsyncInternal(shared map[string]interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		prepRes := a.PrepAsync(shared)
		execResChan := a.execAsyncInternal(prepRes)
		execRes := <-execResChan
		postRes := a.PostAsync(shared, prepRes, execRes)
		result <- postRes
	}()
	return result
}

// execAsyncInternal implements async retry logic
func (a *AsyncNode) execAsyncInternal(prepRes interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		for i := 0; i < a.maxRetries; i++ {
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
				res := a.ExecAsync(prepRes)
				if err == nil {
					result <- res
					return
				}
			}()

			if err == nil {
				return
			}

			if i == a.maxRetries-1 {
				result <- a.ExecFallbackAsync(prepRes, err)
				return
			}

			if a.wait > 0 {
				time.Sleep(a.wait)
			}
		}
	}()
	return result
}

// RunInternal overrides the synchronous run method
func (a *AsyncNode) runInternal(shared map[string]interface{}) interface{} {
	return errors.New("use RunAsync")
}

// AsyncBatchNode processes items in batches asynchronously
type AsyncBatchNode struct {
	*AsyncNode
}

// NewAsyncBatchNode creates a new AsyncBatchNode instance
func NewAsyncBatchNode(maxRetries int, wait time.Duration) *AsyncBatchNode {
	return &AsyncBatchNode{
		AsyncNode: NewAsyncNode(maxRetries, wait),
	}
}

// execAsyncInternal processes each item in the batch asynchronously
func (a *AsyncBatchNode) execAsyncInternal(items interface{}) chan interface{} {
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
			resChan := a.AsyncNode.execAsyncInternal(item)
			results[i] = <-resChan
		}
		result <- results
	}()
	return result
}

// AsyncParallelBatchNode processes items in parallel batches
type AsyncParallelBatchNode struct {
	*AsyncNode
}

// NewAsyncParallelBatchNode creates a new AsyncParallelBatchNode instance
func NewAsyncParallelBatchNode(maxRetries int, wait time.Duration) *AsyncParallelBatchNode {
	return &AsyncParallelBatchNode{
		AsyncNode: NewAsyncNode(maxRetries, wait),
	}
}

// execAsyncInternal processes items in parallel
func (a *AsyncParallelBatchNode) execAsyncInternal(items interface{}) chan interface{} {
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

		var wg sync.WaitGroup
		results := make([]interface{}, len(itemsSlice))
		resultChans := make([]chan interface{}, len(itemsSlice))

		for i, item := range itemsSlice {
			wg.Add(1)
			resultChans[i] = make(chan interface{}, 1)
			go func(idx int, itm interface{}) {
				defer wg.Done()
				resChan := a.AsyncNode.execAsyncInternal(itm)
				resultChans[idx] <- <-resChan
			}(i, item)
		}

		go func() {
			wg.Wait()
			for i, ch := range resultChans {
				results[i] = <-ch
				close(ch)
			}
			result <- results
		}()
	}()
	return result
}

// AsyncFlow orchestrates async node execution
type AsyncFlow struct {
	*Flow
	*AsyncNode
}

// NewAsyncFlow creates a new AsyncFlow instance
func NewAsyncFlow(start interface{}) *AsyncFlow {
	return &AsyncFlow{
		Flow:      NewFlow(start),
		AsyncNode: NewAsyncNode(1, 0),
	}
}

// orchestrateAsync manages the async flow of execution
func (a *AsyncFlow) orchestrateAsync(shared map[string]interface{}, params map[string]interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		if a.startNode == nil {
			result <- nil
			return
		}

		if params == nil {
			params = make(map[string]interface{})
			for k, v := range a.params {
				params[k] = v
			}
		}

		// Deep copy of startNode would be implemented here
		// For simplicity, we're using the original node
		curr, ok := a.startNode.(*BaseNode)
		if !ok {
			result <- nil
			return
		}

		var lastAction interface{}
		for curr != nil {
			curr.SetParams(params)

			// Check if current node is async
			if asyncNode, isAsync := interface{}(curr).(*AsyncNode); isAsync {
				resChan := asyncNode.runAsyncInternal(shared)
				lastAction = <-resChan
			} else {
				lastAction = curr.runInternal(shared)
			}

			nextNode := a.GetNextNode(curr, fmt.Sprintf("%v", lastAction))
			if nextNode == nil {
				break
			}

			nextBaseNode, ok := nextNode.(*BaseNode)
			if !ok {
				break
			}
			curr = nextBaseNode
		}

		result <- lastAction
	}()
	return result
}

// runAsyncInternal executes the async flow
func (a *AsyncFlow) runAsyncInternal(shared map[string]interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		prepResChan := a.PrepAsync(shared)
		prepRes := prepResChan
		orchResChan := a.orchestrateAsync(shared, nil)
		orchRes := <-orchResChan
		postResChan := a.PostAsync(shared, prepRes, orchRes)
		result <- postResChan
	}()
	return result
}

// PostAsync processes results after async flow execution
func (a *AsyncFlow) PostAsync(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	return execRes
}

// AsyncBatchFlow processes batches asynchronously
type AsyncBatchFlow struct {
	*AsyncFlow
}

// NewAsyncBatchFlow creates a new AsyncBatchFlow instance
func NewAsyncBatchFlow(start interface{}) *AsyncBatchFlow {
	return &AsyncBatchFlow{
		AsyncFlow: NewAsyncFlow(start),
	}
}

// runAsyncInternal processes each batch item through the async flow
func (a *AsyncBatchFlow) runAsyncInternal(shared map[string]interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		prepResChan := a.PrepAsync(shared)
		prepRes := prepResChan

		prepSlice, ok := prepRes.([]interface{})
		if !ok || prepSlice == nil {
			prepSlice = []interface{}{}
		}

		for _, bp := range prepSlice {
			bpMap, ok := bp.(map[string]interface{})
			if !ok {
				continue
			}

			params := make(map[string]interface{})
			for k, v := range a.params {
				params[k] = v
			}
			for k, v := range bpMap {
				params[k] = v
			}

			orchResChan := a.orchestrateAsync(shared, params)
			<-orchResChan // Wait for completion but discard result
		}

		postResChan := a.PostAsync(shared, prepRes, nil)
		result <- postResChan
	}()
	return result
}

// AsyncParallelBatchFlow processes batches in parallel
type AsyncParallelBatchFlow struct {
	*AsyncFlow
}

// NewAsyncParallelBatchFlow creates a new AsyncParallelBatchFlow instance
func NewAsyncParallelBatchFlow(start interface{}) *AsyncParallelBatchFlow {
	return &AsyncParallelBatchFlow{
		AsyncFlow: NewAsyncFlow(start),
	}
}

// runAsyncInternal processes batch items in parallel
func (a *AsyncParallelBatchFlow) runAsyncInternal(shared map[string]interface{}) chan interface{} {
	result := make(chan interface{}, 1)
	go func() {
		defer close(result)
		prepResChan := a.PrepAsync(shared)
		prepRes := prepResChan

		prepSlice, ok := prepRes.([]interface{})
		if !ok || prepSlice == nil {
			prepSlice = []interface{}{}
		}

		var wg sync.WaitGroup
		for _, bp := range prepSlice {
			wg.Add(1)
			go func(batchParams interface{}) {
				defer wg.Done()
				bpMap, ok := batchParams.(map[string]interface{})
				if !ok {
					return
				}

				params := make(map[string]interface{})
				for k, v := range a.params {
					params[k] = v
				}
				for k, v := range bpMap {
					params[k] = v
				}

				orchResChan := a.orchestrateAsync(shared, params)
				<-orchResChan // Wait for completion but discard result
			}(bp)
		}

		wg.Wait()
		postResChan := a.PostAsync(shared, prepRes, nil)
		result <- postResChan
	}()
	return result
}
