package skillindex

import (
	"context"
	"reflect"
	"testing"
)

func TestContentOpenCarriesNameAndDirectory(t *testing.T) {
	t.Parallel()
	got := ContentOpen("tdd", "/skills/tdd")
	want := `<skill_content name="tdd" directory="/skills/tdd">`
	if got != want {
		t.Fatalf("ContentOpen = %q, want %q", got, want)
	}
	if !IsContentBlock(got) {
		t.Fatalf("IsContentBlock(%q) = false, want true", got)
	}
	if IsContentBlock("</skill_content>") {
		t.Fatal("IsContentBlock matched only a closing tag")
	}
}

func TestContentNamesExtractsEveryActivation(t *testing.T) {
	t.Parallel()
	content := "leading\n" +
		ContentOpen("tdd", "/s/tdd") + "\nbody\n</skill_content>\n" +
		ContentOpen("web-access", "/s/web-access") + "\nbody\n</skill_content>\n"
	got := ContentNames(content)
	want := []string{"tdd", "web-access"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ContentNames = %v, want %v", got, want)
	}
}

func TestContentNamesIgnoresPlainText(t *testing.T) {
	t.Parallel()
	if got := ContentNames("no activation here"); got != nil {
		t.Fatalf("ContentNames = %v, want nil", got)
	}
}

func TestActiveSkillsRoundTripThroughContext(t *testing.T) {
	t.Parallel()
	ctx := WithActiveSkills(context.Background(), []string{"tdd", "web-access"})
	if !ActiveSkills(ctx)["tdd"] || !ActiveSkills(ctx)["web-access"] {
		t.Fatalf("ActiveSkills = %v, want tdd and web-access", ActiveSkills(ctx))
	}
	if ActiveSkills(context.Background())["tdd"] {
		t.Fatal("base context reports an active skill")
	}
}
