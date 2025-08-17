package main

import (
	"context"
	"fmt"
	"strings"
	_ "strings"

	"github.com/google/generative-ai-go/genai"
	agent "github.com/utkarsh-cpu/go_agent"
	"gopkg.in/yaml.v2"
)

// DecideAction is a node that decides whether to search or answer based on the context
type DecideAction struct {
	agent.Node
	LlmModel *genai.GenerativeModel
	Ctx      context.Context
}

// Decision represents the parsed YAML response from the LLM
type Decision struct {
	Thinking    string `yaml:"thinking"`
	Action      string `yaml:"action"`
	Reason      string `yaml:"reason"`
	Answer      string `yaml:"answer,omitempty"`
	SearchQuery string `yaml:"search_query,omitempty"`
}

func (n DecideAction) Prep(shared agent.SharedData) any {
	// Get the current context (default to "No previous search" if none exists)
	context, ok := shared["context"]
	if !ok {
		context = "No previous search"
	}

	// Get the question from the shared store
	question, ok := shared["question"]
	if !ok {
		return fmt.Errorf("question not found in shared data")
	}

	// Return both for the exec step
	return []any{question, context}
}

func (n DecideAction) Exec(inputs any) any {
	fmt.Println("DEBUG: DecideAction.Exec called")
	// Cast inputs to the expected type
	inputSlice, ok := inputs.([]any)
	if !ok || len(inputSlice) != 2 {
		return fmt.Errorf("invalid inputs: expected [question, context]")
	}

	question, ok1 := inputSlice[0].(string)
	context, ok2 := inputSlice[1].(string)
	if !ok1 || !ok2 {
		return fmt.Errorf("invalid input types: expected strings")
	}

	fmt.Println("🤔 Agent deciding what to do next...")

	// Create a prompt to help the LLM decide what to do next with proper yaml formatting
	prompt := fmt.Sprintf(`
### CONTEXT
You are a research assistant that can search the web.
Question: %s
Previous Research: %s

### ACTION SPACE
[1] search
  Description: Look up more information on the web
  Parameters:
    - query (str): What to search for

[2] answer
  Description: Answer the question with current knowledge
  Parameters:
    - answer (str): Final answer to the question

## NEXT ACTION
Decide the next action based on the context and available actions.
Return your response in this format:

`+"```yaml"+`
thinking: |
    <your step-by-step reasoning process>
action: search OR answer
reason: <why you chose this action>
answer: <if action is answer>
search_query: <specific search query if action is search>
`+"```"+`
IMPORTANT: Make sure to:
1. Use proper indentation (4 spaces) for all multi-line fields
2. Use the | character for multi-line text fields
3. Keep single-line fields without the | character
`, question, context)

	// Call the LLM to make a decision
	response := SentLlmPrompt(n.LlmModel, n.Ctx, []genai.Part{genai.Text(prompt)})

	// Parse the response to get the decision
	yamlStr := ""
	if strings.Contains(response, "```yaml") {
		yamlParts := strings.Split(response, "```yaml")
		if len(yamlParts) > 1 {
			endParts := strings.Split(yamlParts[1], "```")
			if len(endParts) > 0 {
				yamlStr = strings.TrimSpace(endParts[0])
			}
		}
	} else {
		// If no yaml code block, try to parse the whole response
		yamlStr = response
	}

	var decision Decision
	err := yaml.Unmarshal([]byte(yamlStr), &decision)
	if err != nil {
		return fmt.Errorf("failed to parse LLM response as YAML: %w", err)
	}

	return decision
}

func (n DecideAction) Post(shared agent.SharedData, prepRes any, execRes any) any {
	decision, ok := execRes.(Decision)
	if !ok {
		fmt.Println("Error: Invalid decision format")
		return "error"
	}

	// If LLM decided to search, save the search query
	if decision.Action == "search" {
		shared["search_query"] = decision.SearchQuery
		fmt.Printf("🔍 Agent decided to search for: %s\n", decision.SearchQuery)
	} else {
		// Save the answer as context if LLM gives the answer without searching
		shared["context"] = decision.Answer
		fmt.Printf("💡 Agent decided to answer the question\n")
	}

	// Return the action to determine the next node in the flow
	return decision.Action
}

// SearchWeb is a node that searches the web for information
type SearchWebAgent struct {
	agent.Node
}

func (n SearchWebAgent) Prep(shared agent.SharedData) any {
	// Get the search query from the shared store
	searchQuery, ok := shared["search_query"]
	if !ok {
		return fmt.Errorf("search_query not found in shared data")
	}

	return searchQuery
}

func (n SearchWebAgent) Exec(inputs any) any {
	// Cast inputs to the expected type
	searchQuery, ok := inputs.(string)
	if !ok {
		return fmt.Errorf("invalid input: expected string")
	}

	// Call the search utility function
	fmt.Printf("🌐 Searching the web for: %s\n", searchQuery)
	results := SearchWeb(searchQuery)

	return results
}

func (n SearchWebAgent) Post(shared agent.SharedData, prepRes any, execRes any) any {
	// Cast execRes to the expected type
	results, ok := execRes.(string)
	if !ok {
		fmt.Println("Error: Invalid search results format")
		return "error"
	}

	// Add the search results to the context in the shared store
	previous, _ := shared["context"].(string)
	shared["context"] = previous + "\n\nSEARCH: " + shared["search_query"].(string) + "\nRESULTS: " + results

	fmt.Println("📚 Found information, analyzing results...")

	// Always go back to the decision node after searching
	return "decide"
}

// AnswerQuestion is a node that generates a final answer based on the question and context
type AnswerQuestion struct {
	agent.Node
	LlmModel *genai.GenerativeModel
	Ctx      context.Context
}

// CreateAgentFlow creates and connects the nodes to form a complete agent flow
// The flow works like this:
// 1. DecideAction node decides whether to search or answer
// 2. If search, go to SearchWeb node
// 3. If answer, go to AnswerQuestion node
// 4. After SearchWeb completes, go back to DecideAction
func CreateAgentFlow(llmModel *genai.GenerativeModel, ctx context.Context) *agent.Flow {
	// Create instances of each node
	decide := &DecideAction{
		LlmModel: llmModel,
		Ctx:      ctx,
	}
	decide.Node.Init(3, 1000) // maxRetries=3, wait=1000ms

	search := &SearchWebAgent{}
	search.Node.Init(3, 1000) // maxRetries=3, wait=1000ms

	answer := &AnswerQuestion{
		LlmModel: llmModel,
		Ctx:      ctx,
	}
	answer.Node.Init(3, 1000) // maxRetries=3, wait=1000ms

	// Connect the nodes
	// If DecideAction returns "search", go to SearchWeb
	decide.Next(search.BaseNode, "search")

	// If DecideAction returns "answer", go to AnswerQuestion
	decide.Next(answer.BaseNode, "answer")

	// After SearchWeb completes and returns "decide", go back to DecideAction
	search.Next(decide.BaseNode, "decide")

	// Create and return the flow, starting with the DecideAction node
	flow := &agent.Flow{}
	flow.Init(decide.BaseNode)
	return flow
}

func (n AnswerQuestion) Prep(shared agent.SharedData) any {
	// Get the question from the shared store
	question, ok := shared["question"]
	if !ok {
		return fmt.Errorf("question not found in shared data")
	}

	// Get the context (default to empty string if none exists)
	context, ok := shared["context"]
	if !ok {
		context = ""
	}

	// Return both for the exec step
	return []any{question, context}
}

func (n AnswerQuestion) Exec(inputs any) any {
	// Cast inputs to the expected type
	inputSlice, ok := inputs.([]any)
	if !ok || len(inputSlice) != 2 {
		return fmt.Errorf("invalid inputs: expected [question, context]")
	}

	question, ok1 := inputSlice[0].(string)
	context, ok2 := inputSlice[1].(string)
	if !ok1 || !ok2 {
		return fmt.Errorf("invalid input types: expected strings")
	}

	fmt.Println("✍️ Crafting final answer...")

	// Create a prompt for the LLM to answer the question
	prompt := fmt.Sprintf(`
### CONTEXT
Based on the following information, answer the question.
Question: %s
Research: %s

## YOUR ANSWER:
Provide a comprehensive answer using the research results.
`, question, context)

	// Call the LLM to generate an answer
	answer := SentLlmPrompt(n.LlmModel, n.Ctx, []genai.Part{genai.Text(prompt)})
	return answer
}

func (n AnswerQuestion) Post(shared agent.SharedData, prepRes any, execRes any) any {
	// Cast execRes to the expected type
	answer, ok := execRes.(string)
	if !ok {
		fmt.Println("Error: Invalid answer format")
		return "error"
	}

	// Save the answer in the shared store
	shared["answer"] = answer

	fmt.Println("✅ Answer generated successfully")

	// We're done - no need to continue the flow
	return "done"
}
