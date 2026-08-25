package cli

import (
	"strings"
	"testing"
)

// displayWidth measures a line the way a terminal does, so the test asserts what
// a reader sees rather than what len() reports.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWide(r) {
			w += 2
			continue
		}
		w++
	}
	return w
}

// TestWrapMeasuresColumnsNotBytes is the bilingual defect this exists to
// prevent: a byte count wraps Chinese at a third of the intended width, because
// a CJK character is three bytes and two columns.
func TestWrapMeasuresColumnsNotBytes(t *testing.T) {
	const width = 40
	zh := "没有脚本。各贡献者盯着一块共享工作区，当其状态使自己具备条件时才行动；" +
		"每一轮运行所有具备条件者，直到某一轮什么都没改变为止。"

	out := wrap(zh, width, "")
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("text of %d columns did not wrap at %d:\n%s", displayWidth(zh), width, out)
	}
	for i, line := range lines {
		// One column of overflow is allowed, and only for punctuation that may
		// not start a line.
		if w := displayWidth(line); w > width+2 {
			t.Errorf("line %d is %d columns wide, want at most %d:\n%s", i+1, w, width, line)
		}
		if w := displayWidth(line); w < width/2 && i < len(lines)-1 {
			t.Errorf("line %d is only %d columns; the width is being measured in bytes:\n%s",
				i+1, w, line)
		}
	}
	if got := strings.ReplaceAll(strings.ReplaceAll(out, "\n", ""), " ", ""); got != zh {
		t.Errorf("wrapping changed the text:\n got %q\nwant %q", got, zh)
	}
}

// TestWrapBreaksCJKWithoutSpaces: Chinese has no spaces, so a word-based
// wrapper treats a whole paragraph as one unbreakable token and does not wrap
// it at all.
func TestWrapBreaksCJKWithoutSpaces(t *testing.T) {
	zh := strings.Repeat("诊断", 40) // 80 characters, 160 columns, no spaces
	out := wrap(zh, 40, "  ")
	if !strings.Contains(out, "\n") {
		t.Fatalf("a 160-column string with no spaces did not wrap:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if w := displayWidth(strings.TrimLeft(line, " ")); w > 42 {
			t.Errorf("line is %d columns wide:\n%s", w, line)
		}
	}
}

// TestWrapKeepsClosingPunctuationOnTheLineItCloses pins the typesetting rule: a
// line that begins with 。 or ） reads as a mistake to a Chinese reader.
func TestWrapKeepsClosingPunctuationOnTheLineItCloses(t *testing.T) {
	zh := strings.Repeat("证据", 20) + "。"
	for _, line := range strings.Split(wrap(zh, 40, ""), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" {
			continue
		}
		first := []rune(trimmed)[0]
		if strings.ContainsRune("。，、；：？！）】》」』", first) {
			t.Errorf("a line begins with closing punctuation %q:\n%s", string(first), line)
		}
	}
}

// TestWrapPreservesEnglishBehaviour guards the change: English was already
// wrapping correctly and must keep doing so.
func TestWrapPreservesEnglishBehaviour(t *testing.T) {
	en := "A planner directs specialised investigators, one per evidence domain, run concurrently."
	out := wrap(en, 40, "    ")
	for _, line := range strings.Split(out, "\n") {
		if w := displayWidth(strings.TrimLeft(line, " ")); w > 40 {
			t.Errorf("line is %d columns wide:\n%s", w, line)
		}
	}
	if got := strings.Join(strings.Fields(out), " "); got != en {
		t.Errorf("wrapping changed the text:\n got %q\nwant %q", got, en)
	}
}
