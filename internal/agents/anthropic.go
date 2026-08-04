package agents

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/tools/agenttoolset"
)

type anthropicService struct{}

var AnthropicClaude = &anthropicService{}

var anthropicClient = sync.OnceValue(func() anthropic.Client {
	token := os.Getenv("ANTHROPIC_API_KEY")
	if token == "" {
		log.Fatal("anthropic: ANTHROPIC_API_KEY env var is required")
	}
	return anthropic.NewClient(option.WithAPIKey(token))
})

// NOTE: PLEASE READ WHY client.Beta is being used over client.Messages
//  - Tool Runner (client.Beta.Messages.NewToolRunner) — anytime you want the SDK to run the agentic loop
//  instead of writing it manually (for loops/max iterations)
//  - The prebuilt agent toolset (what's used here) or Managed Agents (client.Beta.Agents, client.Beta.Sessions,
//  client.Beta.Environments) — hosted/managed agent primitives
//  - Context editing, compaction, MCP connector, advisor tool, memory tool, cache diagnostics — all beta
//  - Plain single-shot Messages.New calls with no beta features → use client.Messages.New, not
//  client.Beta.Messages.New

//  One thing worth flagging: this file is using ModelClaudeSonnet5 as the model (line 41) with the Tool Runner
//  — that's fine, but note git status shows internal/agents/claude_code.go was deleted and this anthropic.go
//  seems to be its replacement. Let me know if you want me to look at anything else in this file (e.g., the
//  sync.OnceValue client init, or whether error handling around runner.RunToCompletion needs adjustment).
func (s *anthropicService) ResolveIssue(ctx context.Context, repoPath, prompt string) (string, error) {
	client := anthropicClient()

	env := &agenttoolset.AgentToolContext{Workdir: repoPath}

	tools := agenttoolset.BetaAgentToolset20260401(env)
	defer agenttoolset.CloseAll(tools)

	runner := client.Beta.Messages.NewToolRunner(tools, anthropic.BetaToolRunnerParams{
		BetaMessageNewParams: anthropic.BetaMessageNewParams{
			Model:     anthropic.ModelClaudeSonnet5,
			MaxTokens: 8192,
			Messages: []anthropic.BetaMessageParam{
				anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt)),
			},
		},
	})

	message, err := runner.RunToCompletion(ctx)
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}

	var reply strings.Builder
	for _, block := range message.Content {
		if textBlock, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			reply.WriteString(textBlock.Text)
		}
	}
	return reply.String(), nil
}
