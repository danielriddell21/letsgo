package config

import (
	"errors"
	"testing"

	"github.com/danielriddell21/letsgo/modsyntax"
)

func TestDirectiveSetServesBothVocabularies(t *testing.T) {
	for name, set := range map[string]directiveSet{"letsgo.mod": modDirectives, "global": globalDirectives} {
		names := set.names()
		if len(names) == 0 {
			t.Fatalf("%s: no directives", name)
		}
		for _, kw := range names {
			usage, doc, ok := set.lookup(kw)
			if !ok || usage == "" || doc == "" || !set.has(kw) {
				t.Errorf("%s: %s = %q, %q, %v", name, kw, usage, doc, ok)
			}
			err := set.arity("x.mod", &modsyntax.Line{Keyword: kw, P: modsyntax.Position{Line: 1, Col: 1}})
			if err == nil || err.Error() == "" {
				t.Errorf("%s: %s arity error is empty", name, kw)
			}
		}
		if set.has("zzzzzzzz") {
			t.Errorf("%s: has an unknown directive", name)
		}
		var se *modsyntax.SyntaxError
		if err := set.unknown("x.mod", "zzzzzzzz", modsyntax.Position{Line: 1, Col: 1}); !errors.As(err, &se) || se.Suggest != "" {
			t.Errorf("%s: unknown() = %v, want a syntax error without a suggestion", name, err)
		}
	}
}
