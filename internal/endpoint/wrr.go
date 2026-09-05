package endpoint

func nextWRR(items []*member) *member {
	var best *member
	total := 0
	for _, m := range items {
		m.current += m.ep.Weight
		total += m.ep.Weight
		if best == nil || m.current > best.current {
			best = m
		}
	}
	if best != nil {
		best.current -= total
	}
	return best
}
