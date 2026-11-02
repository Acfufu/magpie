package sessions

import (
	"maps"
	"slices"
	"sort"

	"github.com/yetone/magpie/internal/catalog"
)

// SessionUsage is a share of the session's existing total, priced by the
// same effective base prices. Unpriced models are never zero-cost claims.
type SessionUsage struct {
	Tokens
	Models   []Model `json:"models"`
	Cost     float64 `json:"cost"`
	Unpriced int     `json:"unpriced"`
}

// UsageSplit partitions files, not subtrees: a nested subagent's own
// parse occurs exactly once. Other holds auxiliary work (e.g. Claude's
// compaction agents) that isn't the main conversation or a listed child.
type UsageSplit struct {
	Main      SessionUsage  `json:"main"`
	Subagents SessionUsage  `json:"subagents"`
	Other     *SessionUsage `json:"other,omitempty"`
}

func pricedModels(models map[string]*Model, price func(string) *catalog.Price) SessionUsage {
	u := SessionUsage{Models: []Model{}}
	// Stable addition order also keeps the row's cost stable across polls.
	for _, name := range slices.Sorted(maps.Keys(models)) {
		m := *models[name]
		if p := price(m.Model); p != nil {
			// Calls' long-context tiers aren't available in a saved parse.
			m.Cost, m.Priced = p.At(0).CostSplit(m.Input, m.Output, m.CacheRead, m.CacheWrite, m.CacheWrite1h), true
			u.Cost += m.Cost
		} else {
			u.Unpriced++
		}
		u.Tokens.add(m.Tokens)
		u.Models = append(u.Models, m)
	}
	sort.Slice(u.Models, func(i, j int) bool {
		a, b := u.Models[i], u.Models[j]
		if a.Input+a.Output != b.Input+b.Output {
			return a.Input+a.Output > b.Input+b.Output
		}
		return a.Model < b.Model
	})
	return u
}

func addParseModels(models map[string]*Model, st *state) {
	for name, t := range st.Models.all() {
		m := models[name]
		if m == nil {
			m = &Model{Model: name}
			models[name] = m
		}
		m.add(t)
	}
}

func splitUsage(fs []file, subs []Subagent, price func(string) *catalog.Price) *UsageSplit {
	if len(subs) == 0 {
		return nil
	}
	paths := make(map[string]bool, len(subs))
	for _, sub := range subs {
		paths[sub.Path] = true
	}
	main, children, other := map[string]*Model{}, map[string]*Model{}, map[string]*Model{}
	for _, f := range fs {
		st := cache[f.path]
		if st == nil {
			continue
		}
		dest := other
		if f.main {
			dest = main
		} else if paths[f.path] {
			dest = children
		}
		addParseModels(dest, st)
	}
	u := &UsageSplit{Main: pricedModels(main, price), Subagents: pricedModels(children, price)}
	if len(other) > 0 {
		v := pricedModels(other, price)
		u.Other = &v
	}
	return u
}
