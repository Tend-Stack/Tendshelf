package library

// damerauLevenshtein is the optimal-string-alignment distance between a and b (insert, delete,
// substitute, and swap of two neighbours each cost one). It returns max+1 as soon as the distance
// is certain to exceed max, so a long scan over a vocabulary stays cheap.
func damerauLevenshtein(a, b []rune, max int) int {
	la, lb := len(a), len(b)
	if d := la - lb; d > max || -d > max {
		return max + 1
	}
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev2 := make([]int, lb+1)
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				v = min(v, prev2[j-2]+1)
			}
			cur[j] = v
			rowMin = min(rowMin, v)
		}
		if rowMin > max {
			return max + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	if prev[lb] > max {
		return max + 1
	}
	return prev[lb]
}
