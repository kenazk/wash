package primary

import (
	"fmt"

	"github.com/puppetlabs/wash/cmd/internal/find/grammar"
	"github.com/puppetlabs/wash/cmd/internal/find/types"
)

//nolint
func newBooleanPrimary(val bool) *grammar.Atom {
	return grammar.NewAtom([]string{fmt.Sprintf("-%v", val)}, func(tokens []string) (types.Predicate, []string, error) {
		return func(e types.Entry) bool {
			return val
		}, tokens[1:], nil
	})
}

// truePrimary => -true
//nolint
var truePrimary = newBooleanPrimary(true)

// falsePrimary => -false
//nolint
var falsePrimary = newBooleanPrimary(false)
