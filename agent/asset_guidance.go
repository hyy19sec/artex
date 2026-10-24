package agent

import actool "github.com/Autumn-27/norma/tool"

// assetProbeGuidance is code-owned (not seeded), so existing databases receive it
// without a prompt reseed and users cannot accidentally edit it away. It closes the
// information gap between lddscan's probe result sheet (Http Data) and the
// subdomain assets imported from DNS Info: the model used to see an untested
// subdomain and repeat http:80 / https:443 probes that had already failed during
// the scan.
const assetProbeGuidance = "\n\n**lddscan 子域探测口径（重要）**：\n" +
	"- 通过 lddscan 资产同步导入时，Http Data 一定存在；它是对 DNS Info 子域逐个拼接 `http://<domain>:80` 与 `https://<domain>:443` 的**真实探测结果**。\n" +
	"- 因此对 DNS Info 导入的 subdomain：若在 Http Data 中能对应到 status_code，就以该状态码/标题/内容长度为准；**若没有对应记录，表示测绘阶段 curl 没有返回**（超时、连接拒绝、TLS/DNS 失败等），它不是「未测试资产」。\n" +
	"- **注意 `list_assets(type=\"subdomain\")` 返回的 subdomain 行本身没有 HTTP 字段**（status_code / has_js / page_title 只挂在 service 行上）。判断某个子域是否已探测，请按同一 domain 再查 service：`list_assets(type=\"service\", dsl=\"domain==<子域>\")`（或去掉 type，用 `dsl=\"domain==<子域>\"` 看同域全部资产）。查到 HTTP service 就以它的 status_code 为准；查不到才说明测绘阶段 http:80 / https:443 无返回。不要只凭 subdomain 行判断。\n" +
	"- **不要仅因为它是 subdomain 就反复盲试 http:80 / https:443**；除非出现新的端口、路径、协议或业务线索（例如从其他资产的响应/JS/端点中发现新入口），否则优先做其他方向。\n" +
	"- 如果 Http Data 有状态码但当前访问失败，按「状态变化」记录 fact，而不是重复探测。"

// assetProbeGuidanceFor returns the lddscan probe guidance for agents that can
// query assets. Appended after ToolResolve, so it reflects the final permitted
// tool set (including DB overrides/enabled flags).
func assetProbeGuidanceFor(tools []actool.CoreTool) string {
	for _, tool := range tools {
		switch tool.Name() {
		case "list_assets", "list_untested_assets":
			return assetProbeGuidance
		}
	}
	return ""
}
