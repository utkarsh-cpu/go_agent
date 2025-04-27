# Go Agentic Framework

Go library provides a framework for building agentic systems, allowing for the creation of modular, composable, and potentially asynchronous workflows involving interconnected nodes. It's designed to orchestrate complex tasks, including those involving external services like Large Language Models (LLMs) and web search.

## Core Concepts

The framework is built around the concept of **Nodes** and **Flows**:

* **Nodes**: Represent individual units of work or decision points within a workflow.
    * `BaseNode`: The fundamental building block, containing parameters and successors (next nodes based on actions).
    * `Node`: Extends `BaseNode` with retry logic for handling transient errors.
    * `BatchNode`: Processes a slice of items, applying the node's logic to each item.
    * `AsyncNode`: Executes its logic asynchronously, returning a channel. Supports retry logic.
    * `AsyncBatchNode`: Processes a slice of items asynchronously.
    * `AsyncParallelBatchNode`: Processes a slice of items asynchronously and in parallel.
* **Flows**: Orchestrate the execution of a sequence of connected nodes.
    * `Flow`: Manages the execution path, determining the next node based on the action returned by the current node.
    * `BatchFlow`: Executes the defined flow for each item in an input batch.
    * `AsyncFlow`: Orchestrates a flow potentially containing `AsyncNode`s, managing the asynchronous execution.
    * `AsyncBatchFlow`: Runs the asynchronous flow for each item in an input batch.
    * `AsyncParallelBatchFlow`: Runs the asynchronous flow for each item in an input batch in parallel.

Each node typically follows a `Prep` -> `Exec` -> `Post` lifecycle:
1.  `Prep`: Prepares input data, often interacting with a shared data map.
2.  `Exec`: Performs the main work of the node. For flows, this involves orchestrating the sub-nodes.
3.  `Post`: Processes the results and updates the shared data map.

Transitions between nodes are determined by the `action` string returned by a node's execution or post-processing step.

## Example Usage: Research Agent

The `example` directory demonstrates how to use the framework to build a simple research agent:

1.  **`DecideAction` Node**: Takes a question and current context (previous search results). It uses an LLM (like Google's Gemini model via the `google/generative-ai-go` library) to decide whether to `search` for more information or `answer` the question based on the current context. It formulates a specific prompt for the LLM and parses the YAML response to determine the next action and any necessary parameters (like a search query or the final answer).
2.  **`SearchWebNode` Node**: If the decision is to search, this node takes the `search_query` provided by the `DecideAction` node. It executes a web search using the `SearchWeb` utility function (which attempts to scrape Google and Brave search results) and converts the HTML results to Markdown. The results are added to the shared context.
3.  **`AnswerQuestion` Node**: If the decision is to answer, this node takes the question and the accumulated context. It prompts the LLM to generate a comprehensive answer based *only* on the provided information.
4.  **Flow Orchestration**: A `Flow` connects these nodes:
    * Starts with `DecideAction`.
    * If `DecideAction` returns "search", it goes to `SearchWebNode`.
    * If `DecideAction` returns "answer", it goes to `AnswerQuestion`.
    * `SearchWebNode` always returns the action "decide", looping back to `DecideAction` with the updated context.
    * `AnswerQuestion` returns "done", completing the flow.
5.  **Utilities (`utils.go`)**: Provides helper functions for:
    * Setting up the Gemini LLM client (`SetLlmApi`).
    * Sending prompts to the LLM with retry logic (`SentLlmPrompt`).
    * Performing web searches (`SearchWeb`) - *Note: Relies on potentially fragile web scraping*.
    * Converting HTML to Markdown (`ParseHtmlToMarkdown`).

### Running the Example

1.  Set the `GEMINI_API_KEY` environment variable with your API key.
2.  Navigate to the `example` directory.
3.  Run the example with `go run . "Your question here"`. If no question is provided, it uses a default question.
