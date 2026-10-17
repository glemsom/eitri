package skillindex

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestActivationSetClaimsEachNameOnce(t *testing.T) {
	t.Parallel()
	set := NewActivationSet([]string{"already"})
	if set.Claim("already") {
		t.Error("Claim(seeded name) = true, want false")
	}
	if !set.Claim("fresh") {
		t.Error("Claim(inactive name) = false, want true")
	}
	if set.Claim("fresh") {
		t.Error("second Claim(claimed name) = true, want false")
	}
}

func TestActivationSetConcurrentClaimsYieldOneWinner(t *testing.T) {
	t.Parallel()
	set := NewActivationSet(nil)
	const callers = 32
	var wins atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if set.Claim("tdd") {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := wins.Load(); got != 1 {
		t.Fatalf("concurrent claims won by %d callers, want exactly 1", got)
	}
}

func TestClaimActivationWithoutSetTreatsSkillInactive(t *testing.T) {
	t.Parallel()
	if !ClaimActivation(context.Background(), "tdd") {
		t.Fatal("ClaimActivation without a carried set = false, want true")
	}
}

func TestClaimActivationHonoursCarriedSet(t *testing.T) {
	t.Parallel()
	ctx := WithActiveSkills(context.Background(), []string{"seeded"})
	if ClaimActivation(ctx, "seeded") {
		t.Fatal("ClaimActivation(seeded) = true, want false")
	}
	if !ClaimActivation(ctx, "fresh") {
		t.Fatal("ClaimActivation(fresh) = false, want true")
	}
	if ClaimActivation(ctx, "fresh") {
		t.Fatal("second ClaimActivation(fresh) = true, want false")
	}
}

// TestWithActiveSkillsInstallsEmptySet guards the engine's dispatch handoff: an
// empty seed must still carry a claim set, otherwise same-turn calls on a
// turn that starts with no active skill would each see no set and all deliver
// the body.
func TestWithActiveSkillsInstallsEmptySet(t *testing.T) {
	t.Parallel()
	ctx := WithActiveSkills(context.Background(), nil)
	if !ClaimActivation(ctx, "tdd") {
		t.Fatal("first ClaimActivation on an empty-seeded set = false, want true")
	}
	if ClaimActivation(ctx, "tdd") {
		t.Fatal("second ClaimActivation on an empty-seeded set = true, want false")
	}
}

func TestActivationNamesReadsCompleteBlock(t *testing.T) {
	t.Parallel()
	content := ActivationOpen("tdd", "/skills/tdd") + "\nbody\n" + ActivationClose
	if got := ActivationNames(content); !reflect.DeepEqual(got, []string{"tdd"}) {
		t.Fatalf("ActivationNames = %v, want [tdd]", got)
	}
}

func TestActivationNamesSkipsIncompleteBlock(t *testing.T) {
	t.Parallel()
	content := ActivationOpen("tdd", "/skills/tdd") + "\ntruncated body, no closing tag"
	if got := ActivationNames(content); len(got) != 0 {
		t.Fatalf("ActivationNames = %v, want none", got)
	}
}

// TestActivationNamesReadsCompleteAfterIncomplete guards the scan against
// mis-associating a name across an incomplete block boundary: a truncated block
// yields no name and must not swallow the following complete block's.
func TestActivationNamesReadsCompleteAfterIncomplete(t *testing.T) {
	t.Parallel()
	content := ActivationOpen("tdd", "/skills/tdd") + "\ntruncated body" +
		ActivationOpen("res", "/skills/res") + "\nbody\n" + ActivationClose
	if got := ActivationNames(content); !reflect.DeepEqual(got, []string{"res"}) {
		t.Fatalf("ActivationNames = %v, want [res]", got)
	}
}
