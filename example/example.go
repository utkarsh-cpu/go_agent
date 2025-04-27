package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/generative-ai-go/genai"
	agent "github.com/utkarsh-cpu/go_agent"
	"gopkg.in/yaml.v2"
)

// DecideAction node decides whether to search or answer
type DecideAction struct {
	*agent.Node
}

// NewDecideAction creates a new DecideAction node
func NewDecideAction() *DecideAction {
	return &DecideAction{
		Node: agent.NewNode(1, 0),
	}
}

// Prep prepares the context and question for decision-making
func (d *DecideAction) Prep(shared map[string]interface{}) interface{} {
	// Get the current context (default to "No previous search" if none exists)
	contextStr, ok := shared["context"].(string) // Renamed to avoid conflict
	if !ok {
		contextStr = "No previous search"
	}

	// Get the question from the shared store
	question, ok := shared["question"].(string)
	if !ok {
		log.Println("DecideAction.Prep: Question not found or not a string in shared context")
		// Return an error indicator or handle appropriately
		// For now, let's proceed with an empty question, but ideally, this should halt
		question = ""
	}

	// Return both for the exec step
	return []interface{}{question, contextStr}
}

// Exec calls the LLM to decide whether to search or answer
func (d *DecideAction) Exec(prepRes interface{}, shared map[string]interface{}) interface{} { // Add shared map parameter
	inputs, ok := prepRes.([]interface{})
	if !ok || len(inputs) != 2 {
		log.Println("DecideAction.Exec: Invalid prepRes format")
		return map[string]interface{}{"action": "error", "reason": "Internal error: Invalid preparation result"} // Return error map
	}

	question, _ := inputs[0].(string)   // Already checked type in Prep (ideally)
	contextStr, _ := inputs[1].(string) // Already checked type in Prep (ideally)

	// Retrieve model and ctx from shared map
	model, ok := shared["llmModel"].(*genai.GenerativeModel)
	if !ok {
		log.Println("DecideAction.Exec: LLM model not found in shared context")
		return map[string]interface{}{"action": "error", "reason": "Internal error: LLM model configuration missing"}
	}
	ctx, ok := shared["llmCtx"].(context.Context) // Assuming context.Context is the type
	if !ok {
		log.Println("DecideAction.Exec: LLM context not found in shared context")
		return map[string]interface{}{"action": "error", "reason": "Internal error: LLM context configuration missing"}
	}

	fmt.Println("🤔 Agent deciding what to do next...")

	// Create a prompt to help the LLM decide what to do next with proper yaml formatting
	promptText := fmt.Sprintf(`
### CONTEXT
You are a research assistant that can search the web to find relevant information and provide accurate answers.
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

''' yaml
thinking: |
    <your step-by-step reasoning process>
action: search OR answer
reason: <why you chose this action>
answer: <if action is answer>
search_query: <specific search query if action is search>
'''

IMPORTANT: Make sure to:
1. Use proper indentation (4 spaces) for all multi-line fields
2. Use the | character for multi-line text fields
3. Keep single-line fields without the | character
`, question, contextStr)

	// Prepare the prompt for the LLM
	prompt := []genai.Part{genai.Text(promptText)}

	// Call the LLM to make a decision - Pass model, prompt slice, and ctx
	response := SentLlmPrompt(model, ctx, prompt) // Corrected call

	// Check for empty response (e.g., LLM call failed)
	if response == "" {
		log.Println("DecideAction.Exec: Received empty response from LLM")
		// Decide on fallback behavior, e.g., return an error action
		return map[string]interface{}{ // Return error map
			"action": "error",
			"reason": "LLM communication failed or returned empty response",
		}
	}

	// Parse the response to get the decision
	// Extract YAML block carefully
	var yamlStr string
	if strings.Contains(response, "```yaml") {
		parts := strings.SplitN(response, "```yaml", 2)
		if len(parts) > 1 {
			yamlStr = strings.SplitN(parts[1], "```", 2)[0]
		}
	} else if strings.Contains(response, "```") { // Handle cases with just ```
		parts := strings.SplitN(response, "```", 2)
		if len(parts) > 1 {
			yamlStr = strings.SplitN(parts[1], "```", 2)[0]
		}
	} else {
		// Assume the whole response might be YAML if no backticks found
		yamlStr = response
	}
	yamlStr = strings.TrimSpace(yamlStr)

	// Parse YAML
	var decision map[string]interface{}
	err := yaml.Unmarshal([]byte(yamlStr), &decision)
	if err != nil {
		// Log the problematic YAML string for debugging
		log.Printf("Error parsing YAML: %v\nYAML content:\n---\n%s\n---\n", err, yamlStr)
		// Fallback if parsing fails - return an error action
		return map[string]interface{}{ // Return error map
			"action": "error",
			"reason": fmt.Sprintf("Failed to parse LLM response YAML: %v", err),
		}
	}

	// Validate required fields in the decision map
	actionVal, actionOk := decision["action"].(string)
	if !actionOk || (actionVal != "search" && actionVal != "answer") {
		log.Printf("DecideAction.Exec: 'action' field missing, invalid, or not 'search'/'answer' in LLM response: %v", decision["action"])
		return map[string]interface{}{ // Return error map
			"action": "error",
			"reason": "LLM response missing or invalid 'action' field",
		}
	}

	// Validate parameters based on action
	if actionVal == "search" {
		if _, queryOk := decision["search_query"].(string); !queryOk {
			log.Println("DecideAction.Exec: 'search_query' missing or not a string for 'search' action")
			return map[string]interface{}{ // Return error map
				"action": "error",
				"reason": "LLM response missing 'search_query' for 'search' action",
			}
		}
	} else { // actionVal == "answer"
		if _, answerOk := decision["answer"].(string); !answerOk {
			log.Println("DecideAction.Exec: 'answer' missing or not a string for 'answer' action")
			return map[string]interface{}{ // Return error map
				"action": "error",
				"reason": "LLM response missing 'answer' for 'answer' action",
			}
		}
	}

	return decision
}

// Post saves the decision and determines the next step in the flow
func (d *DecideAction) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	decision, ok := execRes.(map[string]interface{})
	if !ok {
		log.Println("DecideAction.Post: execRes is not a map[string]interface{}")
		return "error" // Signal an error state to the flow runner
	}

	action, _ := decision["action"].(string)

	// Handle error action from Exec
	if action == "error" {
		reason, _ := decision["reason"].(string)
		log.Printf("DecideAction.Post: Error occurred during Exec: %s", reason)
		shared["error"] = reason // Store error details
		return "error"           // Propagate error state
	}

	// If LLM decided to search, save the search query
	if action == "search" {
		searchQuery, _ := decision["search_query"].(string)
		shared["search_query"] = searchQuery
		fmt.Printf("🔍 Agent decided to search for: %s\n", searchQuery)
	} else { // action == "answer"
		answer, _ := decision["answer"].(string)
		shared["context"] = answer // save the context if LLM gives the answer without searching
		fmt.Println("💡 Agent decided to answer the question")
	}

	// Return the action to determine the next node in the flow
	return action
}

// SearchWebNode searches the web for information
type SearchWebNode struct {
	*agent.Node
}

// NewSearchWebNode creates a new SearchWebNode
func NewSearchWebNode() *SearchWebNode {
	return &SearchWebNode{
		Node: agent.NewNode(1, 0),
	}
}

// Prep gets the search query from the shared store
func (s *SearchWebNode) Prep(shared map[string]interface{}) interface{} {
	searchQuery, ok := shared["search_query"].(string)
	if !ok || searchQuery == "" {
		log.Println("SearchWebNode.Prep: Search query not found, invalid, or empty in shared context")
		// Return an error or a default query? Returning empty for now, Exec should handle.
		return ""
	}
	return searchQuery
}

// Exec searches the web for the given query
func (s *SearchWebNode) Exec(prepRes interface{}) interface{} {
	searchQuery, ok := prepRes.(string)
	if !ok || searchQuery == "" {
		log.Println("SearchWebNode.Exec: Invalid or empty search query from Prep")
		return "Error: Invalid or empty search query provided."
	}

	// Call the search utility function
	fmt.Printf("🌐 Searching the web for: %s\n", searchQuery)
	results := SearchWeb(searchQuery)
	if results == "" {
		log.Println("SearchWebNode.Exec: Web search returned empty results.")
		// Decide how to handle empty search results, maybe return a specific message
		return "Search completed, but no results were found or retrieved."
	}
	return results
}

// Post saves the search results and goes back to the decision node
func (s *SearchWebNode) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	results, ok := execRes.(string)
	if !ok {
		log.Println("SearchWebNode.Post: execRes is not a string")
		// Handle case where Exec returned an error string or unexpected type
		shared["error"] = "Internal error: Search execution failed or returned unexpected result."
		shared["context"] = fmt.Sprintf("%s\n\nSearch Error: %v", shared["context"], execRes) // Append error info
		return "error"                                                                        // Or maybe decide to retry?
	}

	searchQuery, _ := shared["search_query"].(string) // Query should still be there

	// Add the search results to the context in the shared store
	previousContext, _ := shared["context"].(string)
	// Limit context size if necessary to avoid overly large prompts
	newContext := previousContext + "\n\nSEARCH: " + searchQuery + "\nRESULTS:\n" + results
	// Example: Truncate if too long (implement proper truncation logic if needed)
	// maxContextLen := 4000
	// if len(newContext) > maxContextLen {
	// 	newContext = newContext[len(newContext)-maxContextLen:]
	// }
	shared["context"] = newContext

	fmt.Println("📚 Found information, analyzing results...")

	// Always go back to the decision node after searching
	return "decide"
}

// AnswerQuestion node generates the final answer
type AnswerQuestion struct {
	*agent.Node
}

// NewAnswerQuestion creates a new AnswerQuestion node
func NewAnswerQuestion() *AnswerQuestion {
	return &AnswerQuestion{
		Node: agent.NewNode(1, 0),
	}
}

// Prep gets the question and context for answering
func (a *AnswerQuestion) Prep(shared map[string]interface{}) interface{} {
	question, ok := shared["question"].(string)
	if !ok || question == "" {
		log.Println("AnswerQuestion.Prep: Question not found or empty in shared context")
		// Decide how to handle - return error? Use default? Returning empty for now.
		question = ""
	}
	contextStr, ok := shared["context"].(string) // Renamed variable
	if !ok {
		log.Println("AnswerQuestion.Prep: Context not found in shared context, using empty.")
		contextStr = ""
	}

	return []interface{}{question, contextStr}
}

// Exec calls the LLM to generate a final answer
func (a *AnswerQuestion) Exec(prepRes interface{}, shared map[string]interface{}) interface{} { // Add shared map parameter
	inputs, ok := prepRes.([]interface{})
	if !ok || len(inputs) != 2 {
		log.Println("AnswerQuestion.Exec: Invalid prepRes format")
		return "Error: Internal error preparing to answer question."
	}

	question, _ := inputs[0].(string)
	contextStr, _ := inputs[1].(string) // Renamed to avoid conflict with context package

	// Retrieve model and ctx from shared map
	model, ok := shared["llmModel"].(*genai.GenerativeModel)
	if !ok {
		log.Println("AnswerQuestion.Exec: LLM model not found in shared context")
		return "Error: LLM model configuration missing."
	}
	ctx, ok := shared["llmCtx"].(context.Context) // Assuming context.Context is the type
	if !ok {
		log.Println("AnswerQuestion.Exec: LLM context not found in shared context")
		return "Error: LLM context configuration missing."
	}

	fmt.Println("✍️ Crafting final answer...")

	// Create a prompt for the LLM to answer the question
	promptText := fmt.Sprintf(`
### CONTEXT
Based on the following information, answer the question comprehensively.
Question: %s
Research & Context: %s

## YOUR ANSWER:
Provide a detailed and accurate answer based *only* on the provided Research & Context. If the context is insufficient, state that.
`, question, contextStr)

	// Prepare the prompt for the LLM
	prompt := []genai.Part{genai.Text(promptText)}

	// Call the LLM to generate an answer - Use SentLlmPrompt
	answer := SentLlmPrompt(model, ctx, prompt) // Pass model and ctx

	// Check for empty answer from LLM call
	if answer == "" {
		log.Println("AnswerQuestion.Exec: Received empty response from LLM during answer generation")
		return "Error: Failed to generate answer due to LLM communication issue."
	}

	// Basic cleanup - remove potential markdown code fences if LLM adds them
	answer = strings.TrimSpace(answer)
	if strings.HasPrefix(answer, "```") && strings.Contains(answer, "\n") {
		// More robustly remove potential ```markdown ... ``` fences
		firstLineEnd := strings.Index(answer, "\n")
		if firstLineEnd != -1 {
			answer = strings.TrimSpace(answer[firstLineEnd+1:])
		}
	}
	answer = strings.TrimSuffix(answer, "```")
	answer = strings.TrimSpace(answer)

	return answer
}

// Post saves the final answer and completes the flow
func (a *AnswerQuestion) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	answer, ok := execRes.(string)
	if !ok || answer == "" {
		log.Printf("AnswerQuestion.Post: execRes is not a valid string or is empty: %v", execRes)
		// Store the error/invalid result as the answer
		shared["answer"] = fmt.Sprintf("Failed to generate final answer. Error: %v", execRes)
		shared["error"] = "Answer generation failed or produced empty result."
		return "error" // Indicate an error state
	}

	// Save the valid answer in the shared store
	shared["answer"] = answer

	fmt.Println("✅ Answer generated successfully")

	// We're done - signal completion
	return "done"
}

// CreateResearchAgent creates a research agent flow
func CreateResearchAgent() *agent.Flow {
	// Create nodes
	decideAction := NewDecideAction()
	searchWeb := NewSearchWebNode()
	answerQuestion := NewAnswerQuestion()

	// Create flow
	flow := agent.NewFlow(decideAction)

	// Connect nodes
	decideAction.Next(searchWeb, "search")
	decideAction.Next(answerQuestion, "answer")
	searchWeb.Next(decideAction, "decide")

	return flow
}

// RunResearchAgent runs the research agent with a question
func RunResearchAgent(question string) string {
	// --- LLM Initialization (Moved here for encapsulation) ---
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("Warning: GEMINI_API_KEY environment variable not set. Using dummy values.")
		// Handle this case - maybe return an error immediately?
		// For now, let it proceed but LLM calls will fail.
		apiKey = "dummy-key"
	}
	modelName := "gemini-1.5-flash"

	client, model, ctx, err := SetLlmApi(modelName, apiKey)
	if err != nil {
		log.Printf("Failed to initialize LLM API in RunResearchAgent: %v", err)
		return fmt.Sprintf("Error initializing agent: %v", err)
	}
	defer client.Close()

	// --- Agent Execution ---
	researchAgent := CreateResearchAgent()

	// Create shared context and add LLM model/context
	shared := map[string]interface{}{
		"question": question,
		"llmModel": model, // Pass the initialized model
		"llmCtx":   ctx,   // Pass the initialized context
	}

	// Run the flow
	researchAgent.Run(shared) // Pass shared context

	// --- Result Handling ---
	if errVal, ok := shared["error"]; ok {
		log.Printf("Agent flow finished with error: %v", errVal)
		// Return error message or handle as needed
		return fmt.Sprintf("Agent encountered an error: %v", errVal)
	}

	answer, ok := shared["answer"].(string)
	if !ok || answer == "" {
		log.Println("Agent flow completed, but no valid answer was found in shared context.")
		return "Agent finished, but no answer was generated."
	}

	return answer
}

// Remove global LLM variables as they are now handled within RunResearchAgent
/*
var (
	llmModel  *genai.GenerativeModel
	llmCtx    context.Context
	llmClient *genai.Client // To close later
)
*/

// --- Main Function ---
func main() {
	// --- Get Question ---
	question := "What is the capital of France and what is its population?" // Example question
	if len(os.Args) > 1 {
		question = strings.Join(os.Args[1:], " ") // Allow multi-word questions
	}

	fmt.Println("--- Research Agent --- ")
	fmt.Printf("Question: %s\n", question)
	fmt.Println("-------------------- ")

	// --- Run Agent ---
	// LLM setup is now inside RunResearchAgent
	finalAnswer := RunResearchAgent(question)

	// --- Output ---
	fmt.Println("-------------------- ")
	fmt.Println("Final Answer:")
	fmt.Println(finalAnswer)
	fmt.Println("-------------------- ")
}
