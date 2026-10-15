package control

import (
	"fmt"
	"sort"
	"strings"
)

// Markers delimiting the generated blocks in docs/mcp.md. Everything between
// a begin/end pair is rewritten by `cm-mcp -gen-docs` and verified by a test.
const (
	DocsToolsBegin  = "<!-- BEGIN GENERATED: tools -->"
	DocsToolsEnd    = "<!-- END GENERATED: tools -->"
	DocsParityBegin = "<!-- BEGIN GENERATED: parity -->"
	DocsParityEnd   = "<!-- END GENERATED: parity -->"
)

// ToolReferenceMarkdown renders the tool reference table from the registry.
func ToolReferenceMarkdown() string {
	var b strings.Builder
	b.WriteString("| Инструмент | Уровень | Признаки | Что делает |\n|---|---|---|---|\n")
	for _, t := range MCPTools {
		what := t.Description
		if i := strings.Index(what, " Use when:"); i >= 0 {
			what = what[:i]
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", t.Name, t.Level, annotationWords(t.Annotations), escapeCell(what))
	}
	return b.String()
}

// ParityMapMarkdown renders App method → tool for every method a tool covers.
func ParityMapMarkdown() string {
	type row struct {
		method, tool string
		level        AccessLevel
	}
	var rows []row
	for _, t := range MCPTools {
		for _, m := range t.Covers {
			rows = append(rows, row{m, t.Name, t.Level})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].method < rows[j].method })
	var b strings.Builder
	b.WriteString("| Метод `App` | Инструмент | Уровень |\n|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.method, r.tool, r.level)
	}
	return b.String()
}

// SyncDocs returns doc with both generated blocks replaced by fresh output.
func SyncDocs(doc string) (string, error) {
	doc, err := replaceBlock(doc, DocsToolsBegin, DocsToolsEnd, ToolReferenceMarkdown())
	if err != nil {
		return "", err
	}
	return replaceBlock(doc, DocsParityBegin, DocsParityEnd, ParityMapMarkdown())
}

func replaceBlock(doc, begin, end, body string) (string, error) {
	i := strings.Index(doc, begin)
	j := strings.Index(doc, end)
	if i < 0 || j < i {
		return "", fmt.Errorf("docs/mcp.md: markers %q … %q not found", begin, end)
	}
	return doc[:i+len(begin)] + "\n\n" + body + "\n" + doc[j:], nil
}

func annotationWords(a ToolAnnotations) string {
	var w []string
	if a.ReadOnly {
		w = append(w, "только чтение")
	}
	if a.Destructive {
		w = append(w, "разрушительное")
	}
	if a.Idempotent {
		w = append(w, "идемпотентное")
	}
	if a.OpenWorld {
		w = append(w, "запускает модель")
	}
	if len(w) == 0 {
		return "—"
	}
	return strings.Join(w, ", ")
}

func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}
