package generate

import (
	"fmt"
	"sort"
	"strings"
)

// ToolAccessCEL generates CEL match expressions for an AGW tool-access policy.
// Input: map of service account name → list of tool names the agent can access.
// Output: one CEL expression per agent, sorted by agent name for deterministic output.
func ToolAccessCEL(agentTools map[string][]string) []string {
	agents := make([]string, 0, len(agentTools))
	for agent := range agentTools {
		agents = append(agents, agent)
	}
	sort.Strings(agents)

	var exprs []string
	for _, agent := range agents {
		tools := make([]string, len(agentTools[agent]))
		copy(tools, agentTools[agent])
		sort.Strings(tools)

		var toolExpr string
		if len(tools) == 1 {
			toolExpr = fmt.Sprintf(`mcp.tool.name == "%s"`, tools[0])
		} else {
			quoted := make([]string, len(tools))
			for i, t := range tools {
				quoted[i] = fmt.Sprintf(`"%s"`, t)
			}
			toolExpr = fmt.Sprintf(`mcp.tool.name in [%s]`, strings.Join(quoted, ", "))
		}

		expr := fmt.Sprintf(`source.workload.unverified.serviceAccount == "%s" && %s`, agent, toolExpr)
		exprs = append(exprs, expr)
	}
	return exprs
}
