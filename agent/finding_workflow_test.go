package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

type renamedTool struct {
	actool.CoreTool
	name string
}

func (t renamedTool) Name() string { return t.name }

// TestSigningGuidanceOnlyForReportWriters locks the split between recording a
// finding (report_finding: raw evidence is enough) and writing its report
// (update_finding_report: JS/key reconstruction + Python PoC proxy contract).
func TestSigningGuidanceOnlyForReportWriters(t *testing.T) {
	oldPolicy, oldResolve, oldAugment := FindingTrafficBindingEnabled, ToolResolve, ToolAugment
	t.Cleanup(func() { FindingTrafficBindingEnabled, ToolResolve, ToolAugment = oldPolicy, oldResolve, oldAugment })
	ToolAugment = nil
	ToolResolve = nil

	const marker = "签名/加密类漏洞取证要求"
	for _, enabled := range []bool{false, true} {
		on := enabled
		FindingTrafficBindingEnabled = func() bool { return on }
		ts := NewToolSet(nil, "fixture")
		report := ts.addFinding() // report_finding
		update := renamedTool{CoreTool: report, name: "update_finding_report"}

		_, def, cleanup := AugmentTools(t.Context(), "worker", []actool.CoreTool{report})
		cleanup()
		if strings.Contains(def.FindingGuidance, marker) {
			t.Fatalf("report_finding should not get signing guidance (binding=%v)", on)
		}

		_, def, cleanup = AugmentTools(t.Context(), "reporter", []actool.CoreTool{update})
		cleanup()
		if !strings.Contains(def.FindingGuidance, marker) {
			t.Fatalf("update_finding_report missing signing guidance (binding=%v)", on)
		}
		if !strings.Contains(def.FindingGuidance, "verify=False") {
			t.Fatalf("update_finding_report missing verify=False requirement (binding=%v)", on)
		}
	}
}

func TestFindingWorkflowSharedAssemblyAndSwitch(t *testing.T) {
	oldPolicy, oldResolve, oldAugment := FindingTrafficBindingEnabled, ToolResolve, ToolAugment
	t.Cleanup(func() { FindingTrafficBindingEnabled, ToolResolve, ToolAugment = oldPolicy, oldResolve, oldAugment })
	ToolAugment = nil
	on := false
	FindingTrafficBindingEnabled = func() bool { return on }
	ts := NewToolSet(nil, "fixture")
	ToolResolve = func(_ context.Context, _ string, base []actool.CoreTool) []actool.CoreTool {
		out := make([]actool.CoreTool, len(base))
		for i, tool := range base {
			out[i] = DecorateTool(tool, "CUSTOM DESCRIPTION", tool.InputSchema())
		}
		return out
	}
	for _, role := range []string{"worker", "planner", "mainagent", "auto", "pentest", "custom-agent"} {
		for _, enabled := range []bool{false, true, false} {
			on = enabled
			out, def, cleanup := AugmentTools(t.Context(), role, []actool.CoreTool{ts.addFinding(), ts.addHint()})
			cleanup()
			system, _ := deferredSystem("USER CUSTOM PROMPT", def)
			if !strings.HasPrefix(system[0], "USER CUSTOM PROMPT") {
				t.Fatal("custom prompt replaced")
			}
			if strings.Contains(system[0], "traffic_refs") != on {
				t.Fatalf("%s: guidance ignored switch: %v", role, on)
			}
			if on && (!strings.Contains(system[0], "TCP") || !strings.Contains(system[0], "evidence_hint_id")) {
				t.Fatal("missing optional/handoff contract")
			}
			for _, tool := range out {
				if !strings.HasPrefix(tool.Description(), "CUSTOM DESCRIPTION") {
					t.Fatal("custom description replaced")
				}
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				json.Unmarshal(raw, &schema)
				props := schema["properties"].(map[string]any)
				if (props["traffic_refs"] != nil) != on {
					t.Fatalf("%s: binding schema ignored switch", role)
				}
				if tool.Name() == "add_hint" {
					nested := props["hints"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
					if (nested["traffic_refs"] != nil) != on {
						t.Fatal("nested hint schema ignored switch")
					}
				}
			}
		}
	}
	on = true
	ToolResolve = func(context.Context, string, []actool.CoreTool) []actool.CoreTool { return nil }
	out, def, cleanup := AugmentTools(t.Context(), "planner", []actool.CoreTool{ts.addFinding()})
	defer cleanup()
	if len(out) != 0 || def.FindingGuidance != "" {
		t.Fatal("reintroduced disabled tool or its guidance")
	}
}
