package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's ACTIVE constraints as a high-priority block
// appended to the planner/worker system prompt. Operation constraints are
// inlined because they are small and must always apply. Rating clauses are NOT
// inlined (a large rubric can cost thousands of tokens on every run): the block
// only says they exist and points the agent at get_rating_criteria, which returns
// the clause ids/text on demand. Superseded rows are never injected.
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var opAllow, opDeny []string
	ratingCount := 0
	for _, c := range rows {
		if c.Status == "superseded" {
			continue
		}
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		switch c.Scope {
		case "rating":
			// 定级条款不内联进 system prompt（可能几十条、持续占用 token）。
			// 这里只保留计数；需要评级/规划时 agent 自行调用 get_rating_criteria。
			ratingCount++
		default: // operation / legacy empty
			if c.Kind == "allow" {
				opAllow = append(opAllow, "- "+text)
			} else {
				opDeny = append(opDeny, "- "+text)
			}
		}
	}
	if len(opAllow)+len(opDeny) == 0 && ratingCount == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【任务约束（最高优先级；每生成一条意图、每执行一个动作前先自检）】：")
	if len(opAllow)+len(opDeny) > 0 {
		b.WriteString("\n一、操作授权（task_scope 内默认授权；只有下面明确列出的禁止/排除项才跳过）")
		if len(opAllow) > 0 {
			b.WriteString("\n允许：\n")
			b.WriteString(strings.Join(opAllow, "\n"))
		}
		if len(opDeny) > 0 {
			b.WriteString("\n禁止/排除：\n")
			b.WriteString(strings.Join(opDeny, "\n"))
		}
	}
	if ratingCount > 0 {
		b.WriteString("\n二、定级口径：本任务有生效的 rating 条款，但未内联在系统提示词中。" +
			"评级或规划需要判定严重度/是否计入目标时，先调用 get_rating_criteria 读取条款 id/文本；" +
			"report_finding 的 rating_clause_id 引用该工具返回的 id。")
	}
	b.WriteString("\n（task_scope 内的资产默认授权；只有上述明确列出的禁止/排除项才跳过。发现新资产/端口/主机仍按 task_scope 与排除项判断。）")
	return b.String()
}
