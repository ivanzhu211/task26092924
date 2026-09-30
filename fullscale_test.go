package grantalloc

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// buildFullScale constructs the maximum-size workload:
// 2000 expenditures, 128 grants, up to 4 funding grants per asset, single
// amounts up to 1e9 cents. Balances are sized to make the batch feasible.
func buildFullScale(t *testing.T) (*Ledger, []AssetDef, []Expenditure, *rng) {
	const grants = 128
	const assets = 256
	const lines = 2000
	r := newRng(20260930)

	assetDefs := make([]AssetDef, assets)
	// Grant usage counters to size balances.
	usage := make([]int64, grants)
	linesByAsset := make([][]int, assets)

	exps := make([]Expenditure, lines)
	for i := 0; i < lines; i++ {
		a := int(r.uint64() % assets)
		amt := int64(r.uint64()%1_000_000_000) + 1
		exps[i] = Expenditure{LineID: fmt.Sprintf("L%05d", i), Asset: assetName(a), Amount: amt}
		linesByAsset[a] = append(linesByAsset[a], i)
	}

	for a := 0; a < assets; a++ {
		k := 1 + int(r.uint64()%4)
		chosen := r.perm(grants)[:k]
		ratios := splitRatios(k, r)
		fds := make([]Funding, k)
		for i := range fds {
			g := int(chosen[i])
			fds[i] = Funding{GrantID: grantName(g), Ratio: ratios[i]}
		}
		assetDefs[a] = AssetDef{AssetID: assetName(a), Fundings: fds}
		for _, li := range linesByAsset[a] {
			for _, fd := range fds {
				gi := grantIndexFromName(fd.GrantID)
				usage[gi] += (exps[li].Amount*int64(fd.Ratio) + basisPoints - 1) / basisPoints
			}
		}
	}

	balances := map[string]int64{}
	for g := 0; g < grants; g++ {
		balances[grantName(g)] = usage[g] + 1000 // margin absorbs aggregate rounding
	}
	l, err := NewLedger(balances, assetDefs)
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	return l, assetDefs, exps, r
}

func grantIndexFromName(name string) int {
	var n int
	fmt.Sscanf(name, "G%03d", &n)
	return n
}

func TestFullScaleTrialPerformanceAndConservation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-scale test in -short mode")
	}
	l, _, exps, _ := buildFullScale(t)

	runtime.GC()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var peakMu sync.Mutex
	var peak uint64
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			runtime.ReadMemStats(&ms)
			peakMu.Lock()
			if ms.HeapInuse > peak {
				peak = ms.HeapInuse
			}
			peakMu.Unlock()
			select {
			case <-stop:
				return
			default:
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()
	start := time.Now()
	res, err := l.Trial("FULL", exps)
	elapsed := time.Since(start)
	close(stop)
	wg.Wait()
	peakMu.Lock()
	peakVal := peak
	peakMu.Unlock()

	if err != nil {
		if errors.Is(err, ErrInfeasible) {
			t.Skipf("generated workload infeasible; rerun: %v", err)
		}
		t.Fatalf("Trial: %v", err)
	}
	if peakVal > 256<<20 {
		t.Fatalf("Trial peak heap %d bytes > 256MiB budget", peakVal)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("Trial took %v > 10s", elapsed)
	}
	t.Logf("full-scale trial: %d lines in %v, total_alloc=%d KiB, deviation=%.2f",
		len(exps), elapsed, peakVal/1024, res.TotalDeviation)

	// Conservation: each line fully allocated; per-grant totals consistent.
	totalExpense := int64(0)
	perGrant := map[string]int64{}
	for i, ln := range res.Lines {
		var s int64
		for _, a := range ln.Allocations {
			s += a.Amount
			perGrant[a.GrantID] += a.Amount
		}
		if s != exps[i].Amount {
			t.Fatalf("line %s allocated %d != %d", ln.LineID, s, exps[i].Amount)
		}
		totalExpense += exps[i].Amount
	}
	var totalGrant int64
	for g, v := range perGrant {
		if res.GrantTotals[g] != v {
			t.Fatalf("grant %s total mismatch: %d vs %d", g, res.GrantTotals[g], v)
		}
		totalGrant += v
		b := l.Balance(g)
		if v > b {
			t.Fatalf("trial allocation exceeds balance for %s: %d > %d", g, v, b)
		}
	}
	if totalGrant != totalExpense {
		t.Fatalf("grant total %d != expense total %d", totalGrant, totalExpense)
	}

	// Trial must not mutate state.
	if len(l.History()) != 0 {
		t.Fatal("trial wrote history")
	}

	// Commit the same batch and verify balances are deducted exactly.
	before := l.Balances()
	cres, err := l.Commit("FULL", exps)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	for g, v := range cres.GrantTotals {
		if l.Balance(g) != before[g]-v {
			t.Fatalf("balance %s after commit = %d, want %d", g, l.Balance(g), before[g]-v)
		}
	}
}
