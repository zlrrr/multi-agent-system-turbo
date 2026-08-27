// Package core — bilingual text.
//
// Governs: specs/003-switchable-topologies/design-lld.md §1
package core

import "strings"

// Text is a bilingual string. Constitution Art. III requires both languages to
// be present in everything this project ships.
//
// It lives in core rather than in the package that first needed it because two
// unrelated packages — knowledge packs and the topology registry — need exactly
// this and nothing more. A second copy would drift.
type Text struct {
	EN string `yaml:"en" json:"en"`
	ZH string `yaml:"zh" json:"zh"`
}

// In returns the text in the requested language, falling back to English so a
// partially translated third-party contribution still renders.
func (t Text) In(lang string) string {
	if lang == "zh" && strings.TrimSpace(t.ZH) != "" {
		return t.ZH
	}
	if strings.TrimSpace(t.EN) != "" {
		return t.EN
	}
	return t.ZH
}

// Empty reports whether both languages are blank.
func (t Text) Empty() bool { return strings.TrimSpace(t.EN) == "" && strings.TrimSpace(t.ZH) == "" }

// Complete reports whether both languages are present. Validation requires it
// (Constitution Art. III): work only one audience can read is only half
// delivered. Rendering still falls back through In, so a partial translation
// degrades rather than breaking.
func (t Text) Complete() bool {
	return strings.TrimSpace(t.EN) != "" && strings.TrimSpace(t.ZH) != ""
}
