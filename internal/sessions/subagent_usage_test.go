package sessions

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/settings"
)

// Read the public JSON contract so this regression also runs on the old
// code: a missing breakdown is a behavior failure, not a compile failure.
type detailUsage struct {
	Tokens
	Models   []Model `json:"models"`
	Cost     float64 `json:"cost"`
	Unpriced int     `json:"unpriced"`
}
type usageDetails struct {
	Subagents []struct {
		ID     string `json:"id"`
		Parent string `json:"parent"`
		detailUsage
	} `json:"subagents"`
	Split *struct {
		Main      detailUsage  `json:"main"`
		Subagents detailUsage  `json:"subagents"`
		Other     *detailUsage `json:"other"`
	} `json:"usage_split"`
}

func decodedUsage(t *testing.T, s Session) usageDetails {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var d usageDetails
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestZCodeUsageBreakdown(t *testing.T) {
	home := setupAgents(t)
	PriceOf = func(_ settings.Settings, model string) (catalog.Price, bool) {
		if model == "glm-5.1" {
			return catalog.Price{Input: 2, Output: 8, CacheRead: 0.2, CacheWrite: 3}, true
		}
		if model == "glm-5.1-air" {
			return catalog.Price{Input: 1, Output: 4}, true
		}
		return catalog.Price{}, false
	}
	db, err := sql.Open("sqlite", filepath.Join(home, ".zcode", "cli", "db", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A nested child spends on the same model as its parent, plus an
	// unpriced one. Each child reports its own calls, never its subtree.
	if _, err := db.Exec(`INSERT INTO session (id, project_id, parent_id, slug, directory, title, title_source, version, time_created, time_updated) VALUES ('ses_nested', 'p', 'ses_zcB', 's', '/work/zc', 'Nested task', 'ai', '0.16.9', 1790496010000, 1790496020000)`); err != nil {
		t.Fatal(err)
	}
	ocInsert(t, db, "ses_nested", "msg_nested", 1790496010000, 1790496020000, `{"id":"msg_nested","role":"assistant","modelId":"glm-5.1-air","tokens":{"input":20,"output":3,"cache":{"read":0,"write":0}},"time":{"created":1790496010000,"completed":1790496020000}}`)
	ocInsert(t, db, "ses_nested", "msg_unknown", 1790496010000, 1790496020000, `{"id":"msg_unknown","role":"assistant","modelId":"unknown-model","tokens":{"input":7,"output":2,"cache":{"read":0,"write":0}},"time":{"created":1790496010000,"completed":1790496020000}}`)
	s := find(t, List(0), "zcode", "ses_zcA")
	d := decodedUsage(t, s)
	if d.Split == nil || len(d.Subagents) != 2 {
		t.Fatalf("need main/child usage and two children: %+v", d)
	}
	if d.Split.Main.Tokens != (Tokens{300, 40, 1000, 200, 0}) || d.Split.Subagents.Tokens != (Tokens{77, 10, 0, 0, 0}) || d.Split.Other != nil {
		t.Fatalf("usage split: %+v", d.Split)
	}
	if !near(d.Split.Main.Cost, 0.00172) || !near(d.Split.Subagents.Cost, 0.000102) || d.Split.Subagents.Unpriced != 1 || s.Unpriced != 1 {
		t.Fatalf("priced and unpriced model shares: %+v session=%+v", d.Split, s)
	}
	for _, sub := range d.Subagents {
		switch sub.ID {
		case "ses_zcB":
			if sub.Input != 50 || sub.Output != 5 || sub.Parent != "" || !near(sub.Cost, 0.00007) {
				t.Fatalf("direct child includes descendants: %+v", sub)
			}
		case "ses_nested":
			if sub.Input != 27 || sub.Output != 5 || sub.Parent != "ses_zcB" || sub.Unpriced != 1 {
				t.Fatalf("nested child: %+v", sub)
			}
		default:
			t.Fatalf("unexpected child %+v", sub)
		}
	}
	total := d.Split.Main.Tokens
	total.add(d.Split.Subagents.Tokens)
	if total != s.Tokens || !near(s.Cost, d.Split.Main.Cost+d.Split.Subagents.Cost) {
		t.Fatalf("split changes total: %+v %+v", total, s)
	}
	// Model aliases are applied after pricing to every level, and don't
	// mutate the kept session when the alias is removed.
	if _, err := provider.SetUsageAlias("glm-5.1-air", "glm-5.1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = provider.SetUsageAlias("glm-5.1-air", "") })
	a := decodedUsage(t, find(t, List(0), "zcode", "ses_zcA"))
	if a.Subagents[0].Models[0].Model != "glm-5.1" || !near(a.Subagents[0].Cost, d.Subagents[0].Cost) {
		t.Fatalf("child alias changed price or was missed: %+v", a.Subagents)
	}
	if _, err := provider.SetUsageAlias("glm-5.1-air", ""); err != nil {
		t.Fatal(err)
	}
	b := decodedUsage(t, find(t, List(0), "zcode", "ses_zcA"))
	if b.Subagents[0].Models[0].Model != "glm-5.1-air" {
		t.Fatalf("alias mutated cached child: %+v", b.Subagents)
	}
}

func TestClaudeUsageSplitKeepsAuxiliary(t *testing.T) {
	claude, _ := setup(t)
	const id = "44444444-2222-3333-4444-555555555555"
	dir := filepath.Join(claude, "projects", "-work-shares")
	writeFile(t, filepath.Join(dir, id+".jsonl"), aliasMsg("main", "gpt-6-astra", "2026-09-25T10:00:00Z", 10))
	subs := filepath.Join(dir, id, "subagents")
	writeFile(t, filepath.Join(subs, "agent-child.jsonl"), aliasMsg("child", "gpt-6-astra", "2026-09-25T10:01:00Z", 20))
	writeFile(t, filepath.Join(subs, "agent-acompact-run.jsonl"), aliasMsg("compact", "gpt-6-astra", "2026-09-25T10:02:00Z", 3))
	s := find(t, List(0), "claude", id)
	d := decodedUsage(t, s)
	if d.Split == nil || d.Split.Other == nil || len(d.Subagents) != 1 {
		t.Fatalf("compaction is auxiliary, not a child: %+v", d)
	}
	if d.Split.Main.Input != 10 || d.Split.Subagents.Input != 20 || d.Split.Other.Input != 3 || s.Input != 33 {
		t.Fatalf("every file counts once: %+v session=%+v", d.Split, s)
	}
	if d.Subagents[0].Input != 20 || !near(d.Subagents[0].Cost, 0.0002) || !near(d.Split.Other.Cost, 0.00003) {
		t.Fatalf("child and auxiliary costs: %+v", d)
	}
}
