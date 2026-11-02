package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's ACTIVE constraints as a high-priority block
// appended to the planner/worker system prompt. The three scopes are intentionally
// rendered separately:
//   - operation: authorization boundary (what may / may not be done);
//   - rating:    grading reference (what counts as critical/high/...), NOT a
//     permission list;
//   - report:    report/evidence requirements applied at report time.
//
// Mixing rating clauses into "允许/禁止的操作" used to make the planner treat the
// whole ByteDance rubric as an authorization whitelist and skip every new asset
// or port not explicitly listed. Superseded rows are never injected.
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var opAllow, opDeny, rating, report []string
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
			// 评级条款的 allow/deny 表示「计入/不收取」，不是操作授权。
			if c.Kind == "allow" {
				rating = append(rating, "- [计入] "+text)
			} else {
				rating = append(rating, "- [不收取] "+text)
			}
		case "report":
			report = append(report, "- "+text)
		default: // operation / legacy empty
			if c.Kind == "allow" {
				opAllow = append(opAllow, "- "+text)
			} else {
				opDeny = append(opDeny, "- "+text)
			}
		}
	}
	if len(opAllow)+len(opDeny)+len(rating)+len(report) == 0 {
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
	if len(rating) > 0 {
		b.WriteString("\n二、定级口径（评分参考，不是授权；用于判定严重度和是否计入目标）：\n")
		b.WriteString(strings.Join(rating, "\n"))
	}
	if len(report) > 0 {
		b.WriteString("\n三、报告规则（只在报告/取证阶段生效，不限制探索意图）：\n")
		b.WriteString(strings.Join(report, "\n"))
	}
	b.WriteString("\n（task_scope 内的资产默认授权；只有上述明确列出的禁止/排除项才跳过。发现新资产/端口/主机仍按 task_scope 与排除项判断。）")
	return b.String()
}
