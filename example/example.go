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
		Node: agent.NewNode(1, 10),
	}
}

// Prep prepares the context and question for decision-making
func (d *DecideAction) Prep(shared map[string]interface{}) interface{} {
	contextStr, ok := shared["context"].(string)
	if !ok {
		contextStr = "No previous search"
	}

	question, ok := shared["question"].(string)
	if !ok {
		log.Println("DecideAction.Prep: Question not found in shared context")
		return nil // Important: Return nil to signal an error in Prep
	}

	return []interface{}{question, contextStr}
}

// Exec calls the LLM to decide whether to search or answer
func (d *DecideAction) Exec(prepRes interface{}, shared map[string]interface{}) interface{} {
	if prepRes == nil {
		log.Println("DecideAction.Exec: prepRes is nil, likely an error in Prep")
		return map[string]interface{}{"action": "error", "reason": "Error during preparation"}
	}

	inputs, ok := prepRes.([]interface{})
	if !ok || len(inputs) != 2 {
		log.Println("DecideAction.Exec: Invalid prepRes format")
		return map[string]interface{}{"action": "error", "reason": "Invalid preparation result"}
	}

	question, _ := inputs[0].(string)
	contextStr, _ := inputs[1].(string)

	model, ok := shared["llmModel"].(*genai.GenerativeModel)
	if !ok {
		log.Println("DecideAction.Exec: LLM model not found in shared context")
		return map[string]interface{}{"action": "error", "reason": "LLM model configuration missing"}
	}
	ctx, ok := shared["llmCtx"].(context.Context)
	if !ok {
		log.Println("DecideAction.Exec: LLM context not found in shared context")
		return map[string]interface{}{"action": "error", "reason": "LLM context configuration missing"}
	}

	fmt.Println("🤔 Agent deciding what to do next...")

	// MORE ROBUST PROMPT - Crucial for reliable YAML output
	// Reconstruct the prompt template by concatenating raw strings with regular strings for backtick parts,
	// as raw string literals cannot contain literal backtick characters.
	promptTemplatePart1 := `
### CONTEXT
You are a research assistant that can search the web to find relevant information and provide accurate answers.
Question: %s
Previous Research: %s

### INSTRUCTIONS
1.  Analyze the question and the available research.
2.  Decide whether you have enough information to answer the question accurately.
3.  If you need more information, choose to search the web.
4.  If you have enough information, choose to answer the question.

### ACTION SPACE
Here are the actions you can take:

#### Action 1: search
Description: Look up more information on the web
Parameters:
  query (str): What to search for

#### Action 2: answer
Description: Answer the question with current knowledge
Parameters:
  answer (str): Final answer to the question

### RESPONSE FORMAT
Respond with a YAML block that specifies your decision:
` // End of raw string part 1. Note the newline before concatenation.

	promptTemplatePart2 := `
thinking: |
  <your step-by-step reasoning process>
action: search | answer  # Choose either 'search' or 'answer'
reason: <why you chose this action>
answer: |  # Only include if action is 'answer'
  <your final answer here>
search_query: <specific search query if action is 'search'>
` // End of raw string part 2. Note the newline before concatenation.

	promptTemplatePart3 := `

**IMPORTANT:**
*   Always return a valid YAML block enclosed in triple backticks (` // End of raw string part 3

	promptTemplatePart4 := `).
*   Use proper indentation (2 spaces) for multi-line fields.
*   The 'action' field MUST be either 'search' or 'answer'.
*   If the action is 'search', the 'search_query' field MUST be present and contain a specific search query.
*   If the action is 'answer', the 'answer' field MUST be present and contain the answer.
*   The 'thinking' and 'reason' fields are VERY IMPORTANT for explaining your decision. Be detailed.

NOW, WHAT IS YOUR DECISION?
` // End of raw string part 4

	promptText := fmt.Sprintf(
		promptTemplatePart1+"```yaml"+promptTemplatePart2+"```"+promptTemplatePart3+"```yaml ... ```"+promptTemplatePart4,
		question, contextStr,
	)

	prompt := []genai.Part{genai.Text(promptText)}
	response := SentLlmPrompt(model, ctx, prompt)

	if response == "" {
		log.Println("DecideAction.Exec: Received empty response from LLM")
		return map[string]interface{}{"action": "error", "reason": "LLM communication failed"}
	}

	// Extract YAML block
	yamlStr := ""
	start := strings.Index(response, "```yaml")
	end := strings.LastIndex(response, "```") // Use LastIndex in case of multiple code blocks
	if start != -1 && end != -1 && start < end {
		yamlStr = response[start+len("```yaml") : end]
	} else {
		log.Println("DecideAction.Exec: Could not find YAML block in LLM response")
		return map[string]interface{}{"action": "error", "reason": "Could not extract YAML from LLM response"}
	}

	yamlStr = strings.TrimSpace(yamlStr)

	var decision map[string]interface{}
	err := yaml.Unmarshal([]byte(yamlStr), &decision)
	if err != nil {
		log.Printf("DecideAction.Exec: Error parsing YAML: %v\nYAML content:\n---\n%s\n---\n", err, yamlStr)
		return map[string]interface{}{"action": "error", "reason": fmt.Sprintf("Failed to parse LLM response YAML: %v", err)}
	}

	// VALIDATE the decision map
	actionVal, actionOk := decision["action"].(string)
	if !actionOk || (actionVal != "search" && actionVal != "answer") {
		log.Printf("DecideAction.Exec: Invalid 'action' field in LLM response: %v", decision["action"])
		return map[string]interface{}{"action": "error", "reason": "Invalid or missing 'action' field in LLM response"}
	}

	if actionVal == "search" {
		searchQuery, queryOk := decision["search_query"].(string)
		if !queryOk || searchQuery == "" { // Check for empty string too
			log.Println("DecideAction.Exec: Missing or empty 'search_query' for 'search' action")
			return map[string]interface{}{"action": "error", "reason": "Missing or empty 'search_query' for 'search' action"}
		}
	} else if actionVal == "answer" {
		answer, answerOk := decision["answer"].(string)
		if !answerOk || answer == "" { // Check for empty string too
			log.Println("DecideAction.Exec: Missing or empty 'answer' for 'answer' action")
			return map[string]interface{}{"action": "error", "reason": "Missing or empty 'answer' for 'answer' action"}
		}
	}

	// Log the successful decision (for debugging)
	log.Printf("DecideAction.Exec: LLM Decision: %v", decision)

	return decision
}

// Post saves the decision and determines the next step in the flow
func (d *DecideAction) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	decision, ok := execRes.(map[string]interface{})
	if !ok {
		log.Println("DecideAction.Post: execRes is not a map[string]interface{}")
		return "error"
	}

	action, ok := decision["action"].(string)
	if !ok {
		log.Println("DecideAction.Post: 'action' missing in execRes")
		return "error"
	}

	if action == "error" {
		reason, _ := decision["reason"].(string)
		log.Printf("DecideAction.Post: Error occurred during Exec: %s", reason)
		shared["error"] = reason
		return "error"
	}

	if action == "search" {
		searchQuery, _ := decision["search_query"].(string)
		shared["search_query"] = searchQuery
		fmt.Printf("🔍 Agent decided to search for: %s\n", searchQuery)
	} else if action == "answer" {
		answer, _ := decision["answer"].(string)
		shared["answer"] = answer // Store the direct answer
		fmt.Println("💡 Agent decided to answer the question")
	} else {
		log.Printf("DecideAction.Post: Unknown action: %s", action)
		return "error"
	}

	return action
}

// SearchWebNode searches the web for information
type SearchWebNode struct {
	*agent.Node
}

// NewSearchWebNode creates a new SearchWebNode
func NewSearchWebNode() *SearchWebNode {
	return &SearchWebNode{
		Node: agent.NewNode(1, 10),
	}
}

// Prep gets the search query from the shared store
func (s *SearchWebNode) Prep(shared map[string]interface{}) interface{} {
	searchQuery, ok := shared["search_query"].(string)
	if !ok || searchQuery == "" {
		log.Println("SearchWebNode.Prep: Search query not found in shared context")
		return nil // Signal error in Prep
	}
	return searchQuery
}

// Exec searches the web for the given query
func (s *SearchWebNode) Exec(prepRes interface{}) interface{} {
	if prepRes == nil {
		log.Println("SearchWebNode.Exec: prepRes is nil, likely an error in Prep")
		return "Error: No search query provided."
	}

	searchQuery, ok := prepRes.(string)
	if !ok || searchQuery == "" {
		log.Println("SearchWebNode.Exec: Invalid or empty search query from Prep")
		return "Error: Invalid search query provided."
	}

	fmt.Printf("🌐 Searching the web for: %s\n", searchQuery)
	results := SearchWeb(searchQuery)
	if results == "" {
		log.Println("SearchWebNode.Exec: Web search returned empty results.")
		return "Search completed, but no results were found."
	}
	return results
}

// Post saves the search results and goes back to the decision node
func (s *SearchWebNode) Post(shared map[string]interface{}, prepRes interface{}, execRes interface{}) interface{} {
	results, ok := execRes.(string)
	if !ok {
		log.Println("SearchWebNode.Post: execRes is not a string")
		shared["error"] = "Internal error: Search execution failed."
		return "error"
	}

	searchQuery, _ := shared["search_query"].(string)

	previousContext, _ := shared["context"].(string)
	newContext := previousContext + "\n\nSEARCH: " + searchQuery + "\nRESULTS:\n" + results
	maxContextLen := 3000
	if len(newContext) > maxContextLen {
		newContext = "..." + newContext[len(newContext)-maxContextLen:]
	}
	shared["context"] = newContext

	fmt.Println("📚 Found information, analyzing results...")

	return "decide"
}

// AnswerQuestion node generates the final answer
type AnswerQuestion struct {
	*agent.Node
}

// NewAnswerQuestion creates a new AnswerQuestion node
func NewAnswerQuestion() *AnswerQuestion {
	return &AnswerQuestion{
		Node: agent.NewNode(1, 10),
	}
}

// Prep gets the question and context for answering
func (a *AnswerQuestion) Prep(shared map[string]interface{}) interface{} {
	question, ok := shared["question"].(string)
	if !ok {
		log.Println("AnswerQuestion.Prep: Question not found in shared context")
		return nil // Important: Signal error in Prep
	}

	// Check for a direct answer
	answer, ok := shared["answer"].(string)
	if ok && answer != "" {
		log.Println("AnswerQuestion.Prep: Found direct answer in shared context")
		return []interface{}{question, answer} // Use the direct answer immediately
	}

	// Fallback to context if no direct answer
	contextStr, ok := shared["context"].(string)
	if !ok {
		contextStr = "No context available."
	}

	return []interface{}{question, contextStr}
}

// Exec calls the LLM to generate a final answer
func (a *AnswerQuestion) Exec(prepRes interface{}, shared map[string]interface{}) interface{} {
	if prepRes == nil {
		log.Println("AnswerQuestion.Exec: prepRes is nil, likely an error in Prep")
		return "Error: No data to generate an answer."
	}

	inputs, ok := prepRes.([]interface{})
	if !ok || len(inputs) != 2 {
		log.Println("AnswerQuestion.Exec: Invalid prepRes format")
		return "Error: Internal error preparing to answer question."
	}

	question, _ := inputs[0].(string)
	contextStr, _ := inputs[1].(string)

	model, ok := shared["llmModel"].(*genai.GenerativeModel)
	if !ok {
		log.Println("AnswerQuestion.Exec: LLM model not found in shared context")
		return "Error: LLM model configuration missing."
	}
	ctx, ok := shared["llmCtx"].(context.Context)
	if !ok {
		log.Println("AnswerQuestion.Exec: LLM context not found in shared context")
		return "Error: LLM context configuration missing."
	}

	fmt.Println("✍️ Crafting final answer...")

	// Enhanced prompt for final answer generation
	promptText := fmt.Sprintf(`
### CONTEXT
Based on the following information, answer the question comprehensively.  If the context doesn't contain the information needed to directly answer the question, state that you are unable to answer based on the available information.
Question: %s
Research & Context: %s

## YOUR ANSWER:
Provide a detailed and accurate answer based *only* on the provided Research & Context. If the context is insufficient, state that.
`, question, contextStr)

	prompt := []genai.Part{genai.Text(promptText)}
	answer := SentLlmPrompt(model, ctx, prompt)

	if answer == "" {
		log.Println("AnswerQuestion.Exec: Received empty response from LLM during answer generation")
		return "Error: Failed to generate answer due to LLM communication issue."
	}

	answer = strings.TrimSpace(answer)
	if strings.HasPrefix(answer, "```") && strings.Contains(answer, "\n") {
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
		log.Printf("AnswerQuestion.Post: Invalid execRes - Type: %T, Value: %v", execRes, execRes)
		shared["error"] = "Error: Failed to generate valid answer"
		return "error"
	}

	if strings.HasPrefix(answer, "Error:") {
		shared["error"] = answer
		return "error"
	}

	shared["answer"] = answer

	fmt.Println("✅ Answer generated successfully")

	return "done"
}

// CreateResearchAgent creates a research agent flow
func CreateResearchAgent() *agent.Flow {
	decideAction := NewDecideAction()
	searchWeb := NewSearchWebNode()
	answerQuestion := NewAnswerQuestion()

	flow := agent.NewFlow(decideAction)

	decideAction.Next(searchWeb, "search")
	decideAction.Next(answerQuestion, "answer")
	searchWeb.Next(decideAction, "decide")

	return flow
}

// RunResearchAgent runs the research agent with a question
func RunResearchAgent(question string) string {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("Warning: GEMINI_API_KEY environment variable not set. Using dummy values.")
		apiKey = "dummy-key"
	}
	modelName := "gemini-2.0-flash"

	client, model, ctx, err := SetLlmApi(modelName, apiKey)
	if err != nil {
		log.Printf("Failed to initialize LLM API in RunResearchAgent: %v", err)
		return fmt.Sprintf("Error initializing agent: %v", err)
	}
	defer client.Close()

	researchAgent := CreateResearchAgent()

	shared := map[string]interface{}{
		"question": question,
		"llmModel": model,
		"llmCtx":   ctx,
		"context":  "", // Initialize the context
	}

	fmt.Println("🔄 Starting agent flow...")
	outcome := researchAgent.Run(shared)

	fmt.Println("\n🔍 Final Shared Context:")
	for k, v := range shared {
		if k == "context" {
			fmt.Printf("- %s: [%d chars]\n", k, len(v.(string)))
		} else {
			fmt.Printf("- %s: %v\n", k, v)
		}
	}

	if errVal, ok := shared["error"]; ok {
		log.Printf("Agent flow finished with error: %v", errVal)
		return fmt.Sprintf("Agent encountered an error: %v", errVal)
	}

	answer, ok := shared["answer"].(string)
	if !ok || answer == "" {
		log.Println("Agent flow completed, but no valid answer was found in shared context.")
		return "Agent finished, but no answer was generated."
	}

	// Also return the outcome of the flow
	return fmt.Sprintf("%s\nFlow Outcome: %v", answer, outcome)
}

// Remove global LLM variables as they are now handled within RunResearchAgent

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
