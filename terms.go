package search

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"

	"github.com/grafana/sobek"
	"go.k6.io/k6/js/common"
)

// Terms provides sequential and random access to a list of search terms.
// Accepts a JSON string or a SharedArray:
//
//	search.terms(open("./terms.json"))
//	search.terms(new SharedArray("terms", () => JSON.parse(open("./terms.json"))))
type Terms struct {
	terms []string
	index int
}

// newTerms creates a Terms from a JSON string or array-like JS object.
func (m *ModuleInstance) newTerms(data interface{}) *Terms {
	rt := m.vu.Runtime()
	var terms []string

	switch v := data.(type) {
	case string:
		if err := json.Unmarshal([]byte(v), &terms); err != nil {
			common.Throw(rt, fmt.Errorf("terms: invalid JSON: %v", err))
			return nil
		}
	default:
		obj := rt.ToValue(v).ToObject(rt)
		lengthVal := obj.Get("length")
		if lengthVal == nil || sobek.IsUndefined(lengthVal) {
			common.Throw(rt, fmt.Errorf("terms: expected JSON string or array"))
			return nil
		}
		n := int(lengthVal.ToInteger())
		terms = make([]string, n)
		for i := range n {
			val := obj.Get(strconv.Itoa(i))
			if val != nil && !sobek.IsUndefined(val) {
				terms[i] = val.String()
			}
		}
	}

	if len(terms) == 0 {
		common.Throw(rt, fmt.Errorf("terms: empty"))
		return nil
	}
	return &Terms{terms: terms}
}

// Next returns the next term, cycling sequentially.
func (t *Terms) Next() string {
	term := t.terms[t.index%len(t.terms)]
	t.index++
	return term
}

// Random returns a random term.
func (t *Terms) Random() string {
	return t.terms[rand.IntN(len(t.terms))]
}
