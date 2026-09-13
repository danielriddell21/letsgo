package release

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danielriddell21/letsgo/internal/build"
	"github.com/danielriddell21/letsgo/internal/bytesize"
)

// nearBudget is the fraction of a budget at which a binary is worth
// mentioning. A cap that is only ever discussed on the day it breaks tends to
// break on the day of a release; saying "94% of 15MB" a version earlier is
// the point of writing one down.
const nearBudget = 0.9

// checkBudgets compares each binary against its target's cap.
//
// It runs after the build because a binary's size is not knowable before one
// exists. Every target is checked before anything is reported, so a run says
// which of five targets grew rather than only the first.
func checkBudgets(artifacts []build.Artifact, budgets map[string]bytesize.Size, warnf func(string, ...any)) error {
	if len(budgets) == 0 {
		return nil
	}

	var over, near []string
	for _, a := range artifacts {
		budget, capped := budgets[a.Target]
		if !capped {
			continue
		}

		size := bytesize.Size(a.BinarySize)
		used := float64(size) / float64(budget) * 100

		switch {
		case size > budget:
			over = append(over, fmt.Sprintf("%s is %s, over its %s budget by %s",
				a.Target, size, budget, size-budget))
		case used >= nearBudget*100:
			near = append(near, fmt.Sprintf("%s is %s, %.0f%% of its %s budget",
				a.Target, size, used, budget))
		}
	}

	sort.Strings(near)
	for _, line := range near {
		if warnf != nil {
			warnf("%s", line)
		}
	}

	if len(over) == 0 {
		return nil
	}
	sort.Strings(over)
	return fmt.Errorf("release: size budget exceeded\n  %s", strings.Join(over, "\n  "))
}
