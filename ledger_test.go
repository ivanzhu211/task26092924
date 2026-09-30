package grantalloc

import (
	"errors"
	"math"
	"testing"
)

func mustLedger(t *testing.T, balances map[string]int64, assets []AssetDef) *Ledger {
	t.Helper()
	l, err := NewLedger(balances, assets)
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	return l
}

func allocOf(t *testing.T, ln LineResult, grant string) int64 {
	t.Helper()
	for _, a := range ln.Allocations {
		if a.GrantID == grant {
			return a.Amount
		}
	}
	t.Fatalf("grant %s not allocated on line %s", grant, ln.LineID)
	return 0
}

// 场景一：两笔各1分；首笔A60/B40，次笔仅A；余额各1分。
// 必须首笔全记B、次笔全记A。
func TestScenario1OneCentDifferentAssets(t *testing.T) {
	l := mustLedger(t,
		map[string]int64{"A": 1, "B": 1},
		[]AssetDef{
			{AssetID: "X", Fundings: []Funding{{"A", 6000}, {"B", 4000}}},
			{AssetID: "Y", Fundings: []Funding{{"A", 10000}}},
		})
	batch := []Expenditure{
		{LineID: "L1", Asset: "X", Amount: 1},
		{LineID: "L2", Asset: "Y", Amount: 1},
	}
	res, err := l.Commit("B1", batch)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := allocOf(t, res.Lines[0], "A"); got != 0 {
		t.Fatalf("L1 to A = %d, want 0", got)
	}
	if got := allocOf(t, res.Lines[0], "B"); got != 1 {
		t.Fatalf("L1 to B = %d, want 1", got)
	}
	if got := allocOf(t, res.Lines[1], "A"); got != 1 {
		t.Fatalf("L2 to A = %d, want 1", got)
	}
	if l.Balance("A") != 0 || l.Balance("B") != 0 {
		t.Fatalf("balances = %d,%d want 0,0", l.Balance("A"), l.Balance("B"))
	}
}

// 场景二：两笔各1分；首笔A60/B40，次笔A70/B30；余额各1分。
// 最优：首笔给B、次笔给A，逐笔绝对偏差总和1.8分。
func TestScenario2OptimalDeviation(t *testing.T) {
	l := mustLedger(t,
		map[string]int64{"A": 1, "B": 1},
		[]AssetDef{
			{AssetID: "X", Fundings: []Funding{{"A", 6000}, {"B", 4000}}},
			{AssetID: "Y", Fundings: []Funding{{"A", 7000}, {"B", 3000}}},
		})
	batch := []Expenditure{
		{LineID: "L1", Asset: "X", Amount: 1},
		{LineID: "L2", Asset: "Y", Amount: 1},
	}
	res, err := l.Commit("B1", batch)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := allocOf(t, res.Lines[0], "B"); got != 1 {
		t.Fatalf("L1 to B = %d, want 1", got)
	}
	if allocOf(t, res.Lines[0], "A") != 0 {
		t.Fatalf("L1 to A = %d, want 0", allocOf(t, res.Lines[0], "A"))
	}
	if got := allocOf(t, res.Lines[1], "A"); got != 1 {
		t.Fatalf("L2 to A = %d, want 1", got)
	}
	if allocOf(t, res.Lines[1], "B") != 0 {
		t.Fatalf("L2 to B = %d, want 0", allocOf(t, res.Lines[1], "B"))
	}
	if math.Abs(res.TotalDeviation-1.8) > 1e-9 {
		t.Fatalf("TotalDeviation = %v, want 1.8", res.TotalDeviation)
	}
}

// 场景三：总余额足够，但资助关系受限导致无法合法分摊。
// A/B/C 各有充裕余额；资产X只由A、B资助；但A/B余额不足以覆盖X整批支出。
func TestScenario3InfeasibleDueToFunding(t *testing.T) {
	l := mustLedger(t,
		map[string]int64{"A": 3, "B": 3, "C": 1000},
		[]AssetDef{
			{AssetID: "X", Fundings: []Funding{{"A", 6000}, {"B", 4000}}},
		})
	before := l.Balances()
	batch := []Expenditure{
		{LineID: "L1", Asset: "X", Amount: 10}, // A6 B4
	}
	_, err := l.Commit("B1", batch)
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
	after := l.Balances()
	if len(after) != len(before) {
		t.Fatalf("balance set changed: %v vs %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("balance of %s changed: %d -> %d after rejection", k, v, after[k])
		}
	}
	if len(l.History()) != 0 {
		t.Fatalf("history non-empty after rejection: %d", len(l.History()))
	}
}

// 试算不得修改任何账目，且可重复。
func TestTrialDoesNotMutate(t *testing.T) {
	l := mustLedger(t,
		map[string]int64{"A": 1, "B": 1},
		[]AssetDef{{AssetID: "X", Fundings: []Funding{{"A", 6000}, {"B", 4000}}}})
	batch := []Expenditure{{LineID: "L1", Asset: "X", Amount: 1}}
	r1, err := l.Trial("T1", batch)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := l.Trial("T2", batch)
	if err != nil {
		t.Fatal(err)
	}
	if r1.TotalDeviation != r2.TotalDeviation || l.Balance("A") != 1 || l.Balance("B") != 1 {
		t.Fatalf("trial mutated state")
	}
	if len(l.History()) != 0 {
		t.Fatalf("trial wrote history")
	}
}

// 通用守恒检查：逐笔分配合计=支出额；拨款汇总=逐笔累加；余额恰好扣除。
func checkConservation(t *testing.T, initial map[string]int64, l *Ledger) {
	t.Helper()
	spent := map[string]int64{}
	for _, cb := range l.History() {
		r := cb.Result
		var batchTotal int64
		for _, ln := range r.Lines {
			var lineTotal int64
			for _, a := range ln.Allocations {
				lineTotal += a.Amount
				spent[a.GrantID] += a.Amount
			}
			if lineTotal != ln.Amount {
				t.Fatalf("line %s allocated %d != amount %d", ln.LineID, lineTotal, ln.Amount)
			}
			batchTotal += lineTotal
		}
		var grantTotal int64
		for _, v := range r.GrantTotals {
			grantTotal += v
		}
		if grantTotal != batchTotal {
			t.Fatalf("batch %s grant totals %d != line totals %d", cb.BatchID, grantTotal, batchTotal)
		}
		for g := range r.GrantTotals {
			if spent[g] > initial[g] {
				t.Fatalf("grant %s spent %d exceeds initial %d", g, spent[g], initial[g])
			}
		}
	}
	for g, b0 := range initial {
		if l.Balance(g) != b0-spent[g] {
			t.Fatalf("balance %s = %d, want %d (initial %d spent %d)",
				g, l.Balance(g), b0-spent[g], b0, spent[g])
		}
	}
}

func TestSmallRandomizedConservation(t *testing.T) {
	const grants = 8
	const assets = 20
	balances := map[string]int64{}
	for i := 0; i < grants; i++ {
		balances[grantName(i)] = 5_000_000
	}
	assetsDef := make([]AssetDef, 0, assets)
	rng := newRng(42)
	for a := 0; a < assets; a++ {
		k := 1 + int(rng.uint64()%4)
		chosen := rng.perm(grants)[:k]
		ratios := splitRatios(k, rng)
		fds := make([]Funding, k)
		for i := range fds {
			fds[i] = Funding{GrantID: grantName(int(chosen[i])), Ratio: ratios[i]}
		}
		assetsDef = append(assetsDef, AssetDef{AssetID: assetName(a), Fundings: fds})
	}
	l := mustLedger(t, balances, assetsDef)
	_ = balances
	batches := [][]Expenditure{}
	for b := 0; b < 30; b++ {
		n := 1 + int(rng.uint64()%50)
		lines := make([]Expenditure, n)
		for i := range lines {
			lines[i] = Expenditure{
				LineID: lineID(b, i),
				Asset:  assetName(int(rng.uint64() % assets)),
				Amount: int64(rng.uint64() % 100_000),
			}
		}
		if _, err := l.Commit(batchName(b), lines); err != nil {
			if !errors.Is(err, ErrInfeasible) {
				t.Fatalf("unexpected error: %v", err)
			}
		}
		batches = append(batches, lines)
	}
	checkConservation(t, balances, l)
}
