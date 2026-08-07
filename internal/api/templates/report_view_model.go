package templates

import (
	"github.com/glemsom/eitri/internal/report"
)

// TurnView is the template-side edge projection of a report.Turn. It embeds
// the canonical report model — all model fields are promoted, so templates
// consume report.Turn itself — and adds only the pre-rendered HTML the
// templates render (templates never format markdown; handlers pre-render it
// via makeTurnViews). The duplicated field declarations of the former view
// model are deleted: TurnView derives from the model.
type TurnView struct {
	report.Turn
	ContentHTML   string // pre-rendered Markdown → HTML
	ReasoningHTML string // pre-rendered Markdown → HTML
}

// TurnNumber returns the turn's ordinal. The embedded report.Turn field
// shadows the promoted Turn int field of the same name, so expose it
// explicitly for templates.
func (v TurnView) TurnNumber() int {
	return v.Turn.Turn
}
