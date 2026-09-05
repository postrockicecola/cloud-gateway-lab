package prefixcache

import (
	"strings"
	"sync"

	radix "github.com/armon/go-radix"
)

// PrefixIndexer stores known prompt prefixes in a radix tree and reports
// whether a new prompt shares enough of a cached prefix to count as a
// simulated KV-cache hit.
type PrefixIndexer struct {
	mu        sync.RWMutex
	tree      *radix.Tree
	minMatch  int
	minRatio  float64
}

func New(minMatch int, minRatio float64) *PrefixIndexer {
	if minMatch < 1 {
		minMatch = 16
	}
	if minRatio <= 0 {
		minRatio = 0.5
	}
	return &PrefixIndexer{
		tree:     radix.New(),
		minMatch: minMatch,
		minRatio: minRatio,
	}
}

// Insert records a prompt (or system prefix) so later requests can match it.
func (p *PrefixIndexer) Insert(prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tree.Insert(prefix, struct{}{})
}

// MatchPrefix returns whether prompt shares a cached prefix long enough to
// exceed the configured length and ratio thresholds.
func (p *PrefixIndexer) MatchPrefix(prompt string) (hit bool, matchedLen int) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return false, 0
	}

	p.mu.RLock()
	matched, _, ok := p.tree.LongestPrefix(prompt)
	p.mu.RUnlock()
	if !ok {
		return false, 0
	}

	matchedLen = len(matched)
	if matchedLen < p.minMatch {
		return false, matchedLen
	}
	if float64(matchedLen)/float64(len(prompt)) < p.minRatio && matchedLen < p.minMatch*2 {
		return false, matchedLen
	}
	return true, matchedLen
}
