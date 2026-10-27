// Package learned keeps what a project has learned — skills, rules, bans — as
// plain files in the project (.claude-manager/learned/), next to the database
// that stays the working source of truth (WASTE-TASKS.md WS-09). A file can be
// committed, reverted by git and carried to another machine; nothing is written
// into the project without the caller passing an explicit pick.
package learned

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// Dir is the lessons folder, relative to the project root.
const Dir = ".claude-manager/learned"

// FormatVersion is the file format. A file with a newer one is reported, not read.
const FormatVersion = 1

// Lesson kinds.
const (
	KindSkill = "skill" // a skill: .claude/skills/<name>/SKILL.md
	KindRule  = "rule"  // a rule learned from a recurring error (failure_rules)
	KindBan   = "ban"   // a rule of the generated PreToolUse hook (hook_rules)
)

// Lesson is the file form of one learned item. Everything but Evidence is
// compared (Hash); Evidence is what the lesson was drawn from, carried so an
// import can recreate the row but never a reason for two sides to differ.
type Lesson struct {
	Format int    `json:"format"`
	Kind   string `json:"kind"`
	// Key identifies the lesson inside a project: the skill name, the error
	// key of a rule, "<hook kind>|<param>" of a ban.
	Key    string `json:"key"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Origin string `json:"origin,omitempty"`
	// Body is the skill's SKILL.md, the rule's text or the ban's hint.
	Body string `json:"body"`
	// Sub and Param are the hook kind and its argument of a ban.
	Sub   string `json:"hook_kind,omitempty"`
	Param string `json:"param,omitempty"`
	// Appeared is when the lesson was created; Decided when it was applied or
	// decided (skill: approved_at, rule/ban: decided_at).
	Appeared time.Time  `json:"appeared"`
	Decided  *time.Time `json:"decided,omitempty"`
	// Effect is the measured effect: the stored decision reason when it is
	// JSON, EffectText when it is not.
	Effect     json.RawMessage `json:"effect,omitempty"`
	EffectText string          `json:"effect_text,omitempty"`

	Evidence *Evidence `json:"evidence,omitempty"`

	// Path is the file the lesson was read from; not part of the file.
	Path string `json:"-"`
}

// Evidence is the provenance of a lesson; see Lesson.
type Evidence struct {
	Draft    json.RawMessage `json:"draft,omitempty"`    // skill: the distilled draft
	Source   json.RawMessage `json:"source,omitempty"`   // skill: the signatures it came from
	Examples json.RawMessage `json:"examples,omitempty"` // rule: the error samples
	Runs     int             `json:"runs,omitempty"`     // rule: distinct runs
}

// Ref is the identity of a lesson, usable as a map key.
type Ref struct{ Kind, Key string }

// SyncKey is Ref in the form store.LearnedSyncHashes uses.
func (r Ref) SyncKey() string { return r.Kind + "\x00" + r.Key }

// Ref returns the lesson's identity.
func (l Lesson) Ref() Ref { return Ref{l.Kind, l.Key} }

// Hash is a content hash of everything that makes the lesson what it is: the
// same on both sides exactly when a restore of one from the other would change
// nothing. Evidence, Path and the layout of the file do not count.
func (l Lesson) Hash() string {
	c := l
	c.Evidence, c.Path, c.Format = nil, "", FormatVersion
	c.Appeared = c.Appeared.UTC().Truncate(time.Second)
	if c.Decided != nil {
		d := c.Decided.UTC().Truncate(time.Second)
		c.Decided = &d
	}
	if len(c.Effect) > 0 {
		var buf bytes.Buffer
		if json.Compact(&buf, c.Effect) == nil {
			c.Effect = buf.Bytes()
		}
	}
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Marshal renders the lesson as the file content: indented JSON, a trailing
// newline, no timestamp of its own — the same lesson always gives the same
// bytes, so a commit shows only real changes.
func (l Lesson) Marshal() []byte {
	l.Format = FormatVersion
	l.Path = ""
	l.Appeared = l.Appeared.UTC().Truncate(time.Second)
	if l.Decided != nil {
		d := l.Decided.UTC().Truncate(time.Second)
		l.Decided = &d
	}
	b, _ := json.MarshalIndent(l, "", "  ")
	return append(b, '\n')
}

const (
	maxFileBytes = 1 << 20
	maxKeyRunes  = 400
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// FileName is the lesson's file name: kind, a readable slug of the key and a
// short hash of the whole key, so two keys never share a name.
func FileName(kind, key string) string {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(key), "-"), "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + key))
	name := kind + "-"
	if slug != "" {
		name += slug + "-"
	}
	return name + hex.EncodeToString(sum[:4]) + ".json"
}

// Parse reads and validates a lesson file. A file written by a newer version of
// the format, or one with an unknown kind or an unusable key, is an error: it
// is reported to the user and never imported.
func Parse(data []byte) (Lesson, error) {
	var l Lesson
	if len(data) > maxFileBytes {
		return l, fmt.Errorf("file is larger than %d bytes", maxFileBytes)
	}
	if err := json.Unmarshal(data, &l); err != nil {
		return l, fmt.Errorf("not a lesson file: %w", err)
	}
	if l.Format < 1 || l.Format > FormatVersion {
		return l, fmt.Errorf("format %d is not supported (this version reads %d)", l.Format, FormatVersion)
	}
	if strings.TrimSpace(l.Key) == "" || utf8.RuneCountInString(l.Key) > maxKeyRunes {
		return l, fmt.Errorf("empty or oversized key")
	}
	switch l.Kind {
	case KindSkill:
		if !experience.ValidSkillName(l.Key) {
			return l, fmt.Errorf("invalid skill name %q", l.Key)
		}
		if !liveSkill(l.Status) {
			return l, fmt.Errorf("skill status %q is not exported", l.Status)
		}
	case KindRule:
		if !liveRule(l.Status) {
			return l, fmt.Errorf("rule status %q is not exported", l.Status)
		}
	case KindBan:
		if l.Sub == "" || l.Key != banKey(l.Sub, l.Param) {
			return l, fmt.Errorf("ban key does not match its hook kind and param")
		}
		if !liveBan(l.Status) {
			return l, fmt.Errorf("ban status %q is not exported", l.Status)
		}
	default:
		return l, fmt.Errorf("unknown kind %q", l.Kind)
	}
	return l, nil
}

func liveSkill(s string) bool { return s == store.SkillStatusApproved || s == store.SkillStatusTrial }
func liveRule(s string) bool  { return s == store.FailureRuleKept || s == store.FailureRuleTrial }
func liveBan(s string) bool   { return s == store.HookRuleKept || s == store.HookRuleTrial }

func banKey(sub, param string) string { return sub + "|" + param }

func utc(t time.Time) time.Time { return t.UTC().Truncate(time.Second) }

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := utc(*t)
	return &u
}

// effectOf splits a stored decision reason into the two Lesson fields.
func effectOf(reason string) (json.RawMessage, string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, ""
	}
	var buf bytes.Buffer
	if json.Valid([]byte(reason)) && json.Compact(&buf, []byte(reason)) == nil {
		return json.RawMessage(buf.Bytes()), ""
	}
	return nil, reason
}

// reasonOf is the inverse of effectOf.
func (l Lesson) reasonOf() string {
	if len(l.Effect) > 0 {
		var buf bytes.Buffer
		if json.Compact(&buf, l.Effect) == nil {
			return buf.String()
		}
	}
	return l.EffectText
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
