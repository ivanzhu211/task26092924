package grantalloc

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// 最坏情况构造：所有2000笔都属于同一个4拨款资产，金额10亿分，
// 余额恰好等于理论上界（紧张可行）。
func TestWorstCaseTightBalances(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	ratios := []int{2500, 2500, 2500, 2500}
	fds := make([]Funding, 4)
	balances := map[string]int64{}
	for i := range fds {
		fds[i] = Funding{GrantID: grantName(i), Ratio: ratios[i]}
	}
	lines := make([]Expenditure, 2000)
	// ceil totals for tight balances
	ceils := map[string]int64{}
	for i := range lines {
		amt := int64(1_000_000_000)
		lines[i] = Expenditure{LineID: fmt.Sprintf("L%05d", i), Asset: "X", Amount: amt}
		for _, fd := range fds {
			ceils[fd.GrantID] += (amt*int64(fd.Ratio) + basisPoints - 1) / basisPoints
		}
	}
	for g, v := range ceils {
		balances[g] = v // exactly the aggregate ceil: feasible but tight
	}
	l := mustLedger(t, balances, []AssetDef{{AssetID: "X", Fundings: fds}})
	start := time.Now()
	res, err := l.Trial("W", lines)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Trial: %v", err)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("worst case took %v", elapsed)
	}
	t.Logf("tight-balance worst case: 2000 lines, %v, deviation=%.2f", elapsed, res.TotalDeviation)
	for _, ln := range res.Lines {
		var s int64
		for _, a := range ln.Allocations {
			s += a.Amount
		}
		if s != ln.Amount {
			t.Fatalf("line %s sum %d", ln.LineID, s)
		}
	}
}

// 连续多批满规模提交：余额逐批扣减且全部守恒；余额耗尽后应干净地返回不可行。
func TestRepeatedFullScaleCommits(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	const grants = 128
	const assets = 128
	const batches = 3
	r := newRng(777)
	assetDefs := make([]AssetDef, assets)
	for a := 0; a < assets; a++ {
		perm := r.perm(grants)[:4]
		rs := splitRatios(4, r)
		fds := make([]Funding, 4)
		for i := range fds {
			fds[i] = Funding{GrantID: grantName(int(perm[i])), Ratio: rs[i]}
		}
		assetDefs[a] = AssetDef{AssetID: assetName(a), Fundings: fds}
	}
	// Estimate ceil spend per grant over all batches.
	need := map[string]int64{}
	makeBatch := func(prefix string) []Expenditure {
		lines := make([]Expenditure, 2000)
		for i := range lines {
			a := int(r.uint64() % assets)
			amt := int64(1 + r.uint64()%10_000_000)
			lines[i] = Expenditure{LineID: fmt.Sprintf("%s-L%05d", prefix, i), Asset: assetName(a), Amount: amt}
			for _, fd := range assetDefs[a].Fundings {
				need[fd.GrantID] += (amt*int64(fd.Ratio) + basisPoints - 1) / basisPoints
			}
		}
		return lines
	}
	var allBatches [][]Expenditure
	for b := 0; b < batches; b++ {
		allBatches = append(allBatches, makeBatch(batchName(b)))
	}
	balances := map[string]int64{}
	for g := 0; g < grants; g++ {
		balances[grantName(g)] = need[grantName(g)] + 5
	}
	l := mustLedger(t, balances, assetDefs)
	committed := 0
	for b, lines := range allBatches {
		_, err := l.Commit(batchName(b), lines)
		if err != nil {
			if !errors.Is(err, ErrInfeasible) {
				t.Fatalf("batch %d: %v", b, err)
			}
			break
		}
		committed++
	}
	t.Logf("committed %d/%d full-scale batches, history=%d", committed, batches, len(l.History()))
	checkConservation(t, balances, l)
	if len(l.History()) != committed {
		t.Fatalf("history %d != committed %d", len(l.History()), committed)
	}
}
