package experience

import (
	"sort"

	"claude-manager/internal/store"
)

// CharsPerToken is the coarse chars→tokens conversion factor used across the
// experience layer's cost estimates (LEARN-TASKS.md LN-12). There is no exact
// tokenizer without hitting the API, so this is deliberately a documented
// order-of-magnitude approximation, not a precise count — what matters here
// is which signature/tool dominates a project's tool-output volume, not the
// exact figure.
const CharsPerToken = 4

// EstimateTokens approximates the token cost of a tool result from its
// character count. chars<=0 returns 0 — a call whose result_chars is 0 (no
// result was ever captured, see store.ActionResultChars) must estimate to 0
// tokens rather than a negative or garbage value, so it never distorts a
// caller's total (LEARN-TASKS.md LN-12: "тест что вызовы без результата
// (result_chars = 0) не ломают проценты").
func EstimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return chars / CharsPerToken
}

// attributionWindowDays bounds BuildAttributionReport the same way
// TopSignatures/DurationProfile bound their own windows — "what's burning
// context right now", not a command that was expensive a year ago.
const attributionWindowDays = 90

// SignatureAttribution is one (project, sig) pair's share of tool-output
// volume over the report window (LEARN-TASKS.md LN-12).
type SignatureAttribution struct {
	Sig            string
	Tool           string
	Count          int
	EstTokens      int64
	Share          float64 // fraction of the report's TotalEstTokens; 0 when TotalEstTokens is 0
	AvgResultChars float64
	MaxResultChars int
}

// ToolAttribution is the same volume rolled up by tool alone — the "which
// tool is actually burning context" cut (LEARN-TASKS.md LN-12: "второй срез —
// по инструменту").
type ToolAttribution struct {
	Tool      string
	Count     int
	EstTokens int64
	Share     float64
}

// AttributionReport is BuildAttributionReport's result. TotalEstTokens is the
// sum across *every* signature seen in the window, not just the reported
// top-N BySignature slice, so Share always reflects the whole project's
// volume regardless of how the report was truncated.
type AttributionReport struct {
	TotalEstTokens int64
	BySignature    []SignatureAttribution
	ByTool         []ToolAttribution
}

// BuildAttributionReport aggregates a project's action_signatures
// result_chars into per-signature and per-tool token-attribution stats
// (LEARN-TASKS.md LN-12: "топ-N самых дорогих вызовов ... сигнатура,
// суммарные оценочные токены, доля от общего, средний размер одного
// результата, максимум"), most expensive first. topN<=0 means no limit on
// BySignature; ByTool is never truncated — a project only ever uses a
// handful of distinct tools.
//
// Feeds LN-08's rediscoveryChars (a candidate's evidence is weighted by how
// many result chars a session would read through to rediscover the pattern)
// and is raw material for LN-16's cost-regression alerts.
func BuildAttributionReport(st *store.Store, project string, topN int) (AttributionReport, error) {
	rows, err := st.ActionResultChars(project, attributionWindowDays)
	if err != nil {
		return AttributionReport{}, err
	}

	type acc struct {
		tool  string
		count int
		chars int64
		maxCh int
	}
	sigGroups := make(map[string]*acc)
	var sigOrder []string
	toolGroups := make(map[string]*acc)
	var toolOrder []string
	var totalChars int64

	for _, r := range rows {
		totalChars += int64(r.ResultChars)

		sg, ok := sigGroups[r.Sig]
		if !ok {
			sg = &acc{tool: r.Tool}
			sigGroups[r.Sig] = sg
			sigOrder = append(sigOrder, r.Sig)
		}
		sg.count++
		sg.chars += int64(r.ResultChars)
		if r.ResultChars > sg.maxCh {
			sg.maxCh = r.ResultChars
		}

		tg, ok := toolGroups[r.Tool]
		if !ok {
			tg = &acc{tool: r.Tool}
			toolGroups[r.Tool] = tg
			toolOrder = append(toolOrder, r.Tool)
		}
		tg.count++
		tg.chars += int64(r.ResultChars)
	}

	// Computed once from the true total rather than summed from each group's
	// own EstimateTokens — keeps Share's denominator exact instead of
	// accumulating each group's independent rounding-down error.
	total := int64(EstimateTokens(int(totalChars)))

	share := func(chars int64) float64 {
		if total == 0 {
			return 0
		}
		return float64(EstimateTokens(int(chars))) / float64(total)
	}

	bySig := make([]SignatureAttribution, 0, len(sigOrder))
	for _, sig := range sigOrder {
		g := sigGroups[sig]
		bySig = append(bySig, SignatureAttribution{
			Sig:            sig,
			Tool:           g.tool,
			Count:          g.count,
			EstTokens:      int64(EstimateTokens(int(g.chars))),
			Share:          share(g.chars),
			AvgResultChars: float64(g.chars) / float64(g.count),
			MaxResultChars: g.maxCh,
		})
	}
	sort.Slice(bySig, func(i, j int) bool { return bySig[i].EstTokens > bySig[j].EstTokens })
	if topN > 0 && len(bySig) > topN {
		bySig = bySig[:topN]
	}

	byTool := make([]ToolAttribution, 0, len(toolOrder))
	for _, tool := range toolOrder {
		g := toolGroups[tool]
		byTool = append(byTool, ToolAttribution{
			Tool:      tool,
			Count:     g.count,
			EstTokens: int64(EstimateTokens(int(g.chars))),
			Share:     share(g.chars),
		})
	}
	sort.Slice(byTool, func(i, j int) bool { return byTool[i].EstTokens > byTool[j].EstTokens })

	return AttributionReport{
		TotalEstTokens: total,
		BySignature:    bySig,
		ByTool:         byTool,
	}, nil
}
