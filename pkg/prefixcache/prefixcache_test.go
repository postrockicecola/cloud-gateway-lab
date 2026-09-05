package prefixcache

import "testing"

func TestMatchPrefixHitAndMiss(t *testing.T) {
	idx := New(8, 0.2)
	idx.Insert("You are a helpful assistant")

	hit, n := idx.MatchPrefix("You are a helpful assistant. Translate this to French.")
	if !hit || n < 8 {
		t.Fatalf("expected hit, got hit=%v matchedLen=%d", hit, n)
	}

	hit, n = idx.MatchPrefix("Unrelated prompt about weather")
	if hit {
		t.Fatalf("expected miss, matchedLen=%d", n)
	}
}

func TestMatchPrefixBelowThreshold(t *testing.T) {
	idx := New(32, 0.5)
	idx.Insert("Hi")
	hit, n := idx.MatchPrefix("Hi there, write a long essay")
	if hit {
		t.Fatalf("short prefix should miss, matchedLen=%d", n)
	}
}

func TestMatchPrefixConcurrent(t *testing.T) {
	idx := New(4, 0.1)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			idx.Insert("You are a helpful assistant")
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		idx.MatchPrefix("You are a helpful assistant today")
	}
	<-done
}
