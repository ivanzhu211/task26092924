package grantalloc

import (
	"errors"
	"math"
	"testing"
)

type bruteCand struct {
	grant string
	floor int64
	frac  int64
}

// bruteForceOptimalDFS enumerates every legal floor/ceil choice and returns
// the minimum exact total deviation (cents) and feasibility.
func bruteForceOptimalDFS(t *testing.T, lines []Expenditure, fundings [][]Funding, balances map[string]int64) (float64, bool) {
	t.Helper()

	cands := make([][]bruteCand, len(lines))
	upCount := make([]int, len(lines))
	theoNum := map[string]int64{} // sum amount*ratio, units of 1/10000

	for li, e := range lines {
		var fracSum int64
		for _, fd := range fundings[li] {
			prod := e.Amount * int64(fd.Ratio)
			fl := prod / basisPoints
			fr := prod - fl*basisPoints
			cands[li] = append(cands[li], bruteCand{fd.GrantID, fl, fr})
			fracSum += fr
			theoNum[fd.GrantID] += prod
		}
		upCount[li] = int(fracSum / basisPoints)
	}

	best := math.Inf(1)
	used := map[string]int64{}
	bestPlan := map[string]int64{}

	allocsTable := make([][]int64, len(lines))
	var dfs func(li int)
	dfs = func(li int) {
		if li == len(lines) {
			// Exact deviation from actual allocations vs theoretical shares.
			var devNum int64
			usedCheck := map[string]int64{}
			for x, cs := range cands {
				var lineSum int64
				for i, c := range cs {
					act := allocsTable[x][i]
					lineSum += act
					usedCheck[c.grant] += act
					if act == c.floor {
						devNum += c.frac
					} else if act == c.floor+1 && c.frac > 0 {
						devNum += basisPoints - c.frac
					} else {
						return
					}
				}
				if lineSum != lines[x].Amount {
					return
				}
			}
			for g, num := range theoNum {
				u := usedCheck[g]
				fl := num / basisPoints
				if u < fl || u > fl+1 {
					return
				}
				if u == fl+1 && num%basisPoints == 0 {
					return
				}
				if u > balances[g] {
					return
				}
			}
			for g := range balances {
				if _, ok := theoNum[g]; !ok && usedCheck[g] != 0 {
					return
				}
			}
			used = usedCheck
			if d := float64(devNum) / basisPoints; d < best {
				best = d
				bestPlan = map[string]int64{}
				for g, u := range used {
					bestPlan[g] = u
				}
			}
			return
		}
		cs := cands[li]
		positive := make([]int, 0, len(cs))
		var floorSum int64
		row := make([]int64, len(cs))
		for i, c := range cs {
			row[i] = c.floor
			floorSum += c.floor
			if c.frac > 0 {
				positive = append(positive, i)
			}
		}
		allocsTable[li] = row
		need := upCount[li]
		var choose func(start, left int)
		choose = func(start, left int) {
			if left == 0 {
				dfs(li + 1)
				return
			}
			for j := start; j <= len(positive)-left; j++ {
				i := positive[j]
				row[i]++
				choose(j+1, left-1)
				row[i]--
			}
		}
		if need == 0 {
			dfs(li + 1)
		} else if need <= len(positive) {
			choose(0, need)
		}
	}
	dfs(0)

	return best, !math.IsInf(best, 1)
}

func TestBruteForceOptimalitySmall(t *testing.T) {
	rng := newRng(99)
	for iter := 0; iter < 2000; iter++ {
		nG := 2 + int(rng.uint64()%2) // 2 or 3 grants
		gs := []string{"A", "B", "C"}[:nG]
		bal := map[string]int64{}
		for _, g := range gs {
			bal[g] = int64(rng.uint64() % 7)
		}
		nL := 1 + int(rng.uint64()%4)
		assetsDef := make([]AssetDef, 0, nL)
		exps := make([]Expenditure, nL)
		fmat := make([][]Funding, nL)
		for li := 0; li < nL; li++ {
			k := 2 + int(rng.uint64()%uint64(nG-1))
			perm := rng.perm(nG)
			ratios := splitRatios(k, rng)
			fds := make([]Funding, k)
			for i := range fds {
				fds[i] = Funding{GrantID: gs[perm[i]], Ratio: ratios[i]}
			}
			an := assetName(li)
			assetsDef = append(assetsDef, AssetDef{AssetID: an, Fundings: fds})
			fmat[li] = fds
			exps[li] = Expenditure{LineID: lineID(0, li), Asset: an, Amount: int64(rng.uint64() % 6)}
		}
		l, err := NewLedger(bal, assetsDef)
		if err != nil {
			t.Fatal(err)
		}
		want, wantOK := bruteForceOptimalDFS(t, exps, fmat, bal)
		got, gerr := l.Trial("T", exps)
		if !wantOK {
			if !errors.Is(gerr, ErrInfeasible) {
				t.Fatalf("iter %d: want ErrInfeasible, got %v", iter, gerr)
			}
			continue
		}
		if gerr != nil {
			t.Fatalf("iter %d: %v (brute feasible %v)", iter, gerr, want)
		}
		if math.Abs(got.TotalDeviation-want) > 1e-9 {
			t.Fatalf("iter %d: deviation %v != optimal %v", iter, got.TotalDeviation, want)
		}
	}
}
