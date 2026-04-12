package generate_test

import (
	"testing"

	"github.com/mongodb/mycelium/internal/generate"
	"github.com/stretchr/testify/assert"
)

func TestToolAccessCEL_SingleAgentSingleTool(t *testing.T) {
	agentTools := map[string][]string{
		"github-assistant": {"list_repos"},
	}
	exprs := generate.ToolAccessCEL(agentTools)
	assert.Len(t, exprs, 1)
	assert.Equal(t,
		`source.workload.unverified.serviceAccount == "github-assistant" && mcp.tool.name == "list_repos"`,
		exprs[0])
}

func TestToolAccessCEL_SingleAgentMultipleTools(t *testing.T) {
	agentTools := map[string][]string{
		"github-assistant": {"list_repos", "create_issue"},
	}
	exprs := generate.ToolAccessCEL(agentTools)
	assert.Len(t, exprs, 1)
	// Tools should be sorted alphabetically
	assert.Equal(t,
		`source.workload.unverified.serviceAccount == "github-assistant" && mcp.tool.name in ["create_issue", "list_repos"]`,
		exprs[0])
}

func TestToolAccessCEL_MultipleAgents(t *testing.T) {
	agentTools := map[string][]string{
		"github-assistant": {"list_repos", "create_issue"},
		"multi-tool-agent": {"list_repos"},
	}
	exprs := generate.ToolAccessCEL(agentTools)
	assert.Len(t, exprs, 2)
	// Agents should be sorted alphabetically
	assert.Contains(t, exprs[0], "github-assistant")
	assert.Contains(t, exprs[1], "multi-tool-agent")
}

func TestToolAccessCEL_EmptyInput(t *testing.T) {
	exprs := generate.ToolAccessCEL(map[string][]string{})
	assert.Empty(t, exprs)
}
