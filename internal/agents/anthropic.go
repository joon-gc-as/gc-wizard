package agents

import (
	"context"
	"fmt"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/go-github/v89/github"
	"log"
	"os"
	"strings"
	"sync"
)

type anthropicService struct{}

var AnthropicAgent = &anthropicService{}

// anthropicClient is created lazily (rather than at package init) so that
// importing this package doesn't hard-fail processes, like tests, that never
// actually need an Anthropic client and haven't loaded ANTHROPIC_API_KEY.
var anthropicClient = sync.OnceValue(func() anthropic.Client {
	token := os.Getenv("ANTHROPIC_API_KEY")
	if token == "" {
		log.Fatal("anthropic: ANTHROPIC_API_KEY env var is required")
	}
	return anthropic.NewClient(option.WithAPIKey(token))
})

func (s *anthropicService) ResolveIssue(ctx context.Context, issue *github.Issue) (string, error) {
	client := anthropicClient()
	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		MaxTokens: 1024,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(issue.GetBody())),
		},
		Model: anthropic.ModelClaudeOpus4_8,
	})
	if err != nil {
		return "", fmt.Errorf("anthropic: %w", err)
	}
	var reply strings.Builder
	for _, block := range message.Content {
		if textBlock, ok := block.AsAny().(anthropic.TextBlock); ok {
			reply.WriteString(textBlock.Text)
		}
	}
	return reply.String(), nil
}
