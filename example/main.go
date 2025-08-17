package main

import (
	"fmt"
	"log"
	"os"

	agent "github.com/utkarsh-cpu/go_agent"
)

func main() {
	// Get API key from environment variable
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("GEMINI_API_KEY environment variable not set")
	}

	// Set up the LLM API
	client, model, ctx, err := SetLlmApi("gemini-2.0-flash", apiKey)
	if err != nil {
		log.Fatalf("Failed to set up LLM API: %v", err)
	}
	defer client.Close()

	// Create the agent flow
	flow := CreateAgentFlow(model, ctx)

	// Create shared data with a test question
	shared := agent.SharedData{
		"question": "What is the capital of France?",
	}

	// Run the flow
	fmt.Println("Starting agent flow...")
	fmt.Printf("DEBUG: Flow type: %T\n", flow)
	result := flow.Run(shared)
	fmt.Printf("Flow completed with result: %v\n", result)

	// Print the final answer
	if answer, ok := shared["answer"]; ok {
		fmt.Println("\n=== Final Answer ===")
		fmt.Println(answer)
	} else {
		fmt.Println("No answer was generated.")
	}
}
