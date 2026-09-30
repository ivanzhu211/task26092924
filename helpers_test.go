package grantalloc

import "fmt"

func grantName(i int) string { return fmt.Sprintf("G%03d", i) }
func assetName(i int) string { return fmt.Sprintf("A%04d", i) }
func lineID(b, i int) string { return fmt.Sprintf("B%04d-L%04d", b, i) }
func batchName(b int) string { return fmt.Sprintf("B%04d", b) }

// xorshift rng (deterministic, no math/rand seed dependence).
type rng struct{ state uint64 }

func newRng(seed uint64) *rng {
	if seed == 0 {
		seed = 1
	}
	return &rng{state: seed}
}

func (r *rng) uint64() uint64 {
	x := r.state
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	r.state = x
	return x
}

func (r *rng) perm(n int) []uint64 {
	p := make([]uint64, n)
	for i := range p {
		p[i] = uint64(i)
	}
	for i := n - 1; i > 0; i-- {
		j := int(r.uint64() % uint64(i+1))
		p[i], p[j] = p[j], p[i]
	}
	return p
}

// splitRatios produces k positive basis-point ratios summing exactly to 10000.
func splitRatios(k int, r *rng) []int {
	cuts := make([]int, k-1)
	for i := range cuts {
		cuts[i] = int(r.uint64() % 9999)
	}
	for i := 1; i < len(cuts); i++ {
		for j := i; j > 0 && cuts[j-1] > cuts[j]; j-- {
			cuts[j-1], cuts[j] = cuts[j], cuts[j-1]
		}
	}
	out := make([]int, k)
	prev := 0
	for i := 0; i < k-1; i++ {
		out[i] = cuts[i] + 1 - prev
		prev = cuts[i] + 1
	}
	out[k-1] = 10000 - prev
	return out
}
