package search

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"

	"github.com/grafana/sobek"
	"go.k6.io/k6/js/common"
)

// Global terms cache — shared across all VUs.
var (
	termsCache   = make(map[uint64][]string)
	termsCacheMu sync.RWMutex
)

// Terms provides sequential and random access to a list of search terms.
//
//	db.terms(JSON.parse(open("./terms.json")))
//	db.terms(allTerms.filter(t => !t.includes(" ")))
type Terms struct {
	terms []string
	size  int
	index int
}

// newTerms creates a Terms from a JS array.
// The underlying slice is cached globally so all VUs share one copy.
func (m *ModuleInstance) newTerms(data interface{}) *Terms {
	rt := m.vu.Runtime()

	obj, ok := data.(*sobek.Object)
	if !ok {
		obj = rt.ToValue(data).ToObject(rt)
	}
	if obj == nil {
		common.Throw(rt, fmt.Errorf("terms: expected an array"))
		return nil
	}

	terms := readArrayObject(obj)
	if len(terms) == 0 {
		common.Throw(rt, fmt.Errorf("terms: empty"))
		return nil
	}

	// Cache by content hash so all VUs share one slice.
	key := hashTerms(terms)
	termsCacheMu.RLock()
	cached, ok := termsCache[key]
	termsCacheMu.RUnlock()
	if ok {
		terms = cached
	} else {
		termsCacheMu.Lock()
		if cached, ok = termsCache[key]; ok {
			terms = cached
		} else {
			termsCache[key] = terms
		}
		termsCacheMu.Unlock()
	}

	return &Terms{terms: terms, size: len(terms)}
}

func hashTerms(terms []string) uint64 {
	var h uint64 = 14695981039346656037 // FNV offset basis
	for _, t := range terms {
		for i := 0; i < len(t); i++ {
			h ^= uint64(t[i])
			h *= 1099511628211 // FNV prime
		}
		h ^= 0xff
		h *= 1099511628211
	}
	return h
}

// readArrayObject reads strings from an array-like sobek object.
func readArrayObject(obj *sobek.Object) []string {
	lengthVal := obj.Get("length")
	if lengthVal == nil || sobek.IsUndefined(lengthVal) {
		return nil
	}
	n := int(lengthVal.ToInteger())
	result := make([]string, 0, n)
	for i := range n {
		val := obj.Get(strconv.Itoa(i))
		if val != nil && !sobek.IsUndefined(val) {
			result = append(result, val.String())
		}
	}
	return result
}

// Next returns the next term, cycling sequentially.
func (t *Terms) Next() string {
	term := t.terms[t.index%t.size]
	t.index++
	return term
}

// Random returns a random term.
func (t *Terms) Random() string {
	return t.terms[rand.IntN(t.size)]
}
