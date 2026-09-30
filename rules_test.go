package grantalloc

import (
	"errors"
	"testing"
)

// 校验每个逐笔分配只能是 floor 或 ceil，每个拨款总额也只能是 floor/ceil，
// 且不参与资助的拨款不被分配。
func TestFloorCeilAndFundingRestriction(t *testing.T) {
	balances := map[string]int64{"A": 1_000_000, "B": 1_000_000, "C": 1_000_000}
	assets := []AssetDef{
		{AssetID: "X", Fundings: []Funding{{"A", 3333}, {"B", 6667}}},
		{AssetID: "Z", Fundings: []Funding{{"C", 10000}}},
	}
	l := mustLedger(t, balances, assets)
	rng := newRng(7)
	var exps []Expenditure
	for i := 0; i < 300; i++ {
		exps = append(exps, Expenditure{
			LineID: lineID(0, i), Asset: "X", Amount: int64(rng.uint64()%1000) + 1,
		})
	}
	res, err := l.Commit("BX", exps)
	if err != nil {
		t.Fatal(err)
	}

	aggA, aggB := int64(0), int64(0)
	var theoA, theoB int64
	for i, ln := range res.Lines {
		amt := exps[i].Amount
		fa, fb := amt*3333/basisPoints, amt*6667/basisPoints
		theoA += amt * 3333
		theoB += amt * 6667
		a, b := allocOf(t, ln, "A"), allocOf(t, ln, "B")
		if !(a == fa || a == fa+1) {
			t.Fatalf("line %s A=%d not floor/ceil of %d", ln.LineID, a, fa)
		}
		if !(b == fb || b == fb+1) {
			t.Fatalf("line %s B=%d not floor/ceil of %d", ln.LineID, b, fb)
		}
		if a+b != amt {
			t.Fatalf("line %s sum %d != %d", ln.LineID, a+b, amt)
		}
		aggA += a
		aggB += b
		if _, ok := findAlloc(ln, "C"); ok {
			t.Fatalf("non-funding grant C allocated on line %s", ln.LineID)
		}
	}
	fA, cA := theoA/basisPoints, (theoA+basisPoints-1)/basisPoints
	fB, cB := theoB/basisPoints, (theoB+basisPoints-1)/basisPoints
	if !(aggA == fA || aggA == cA) {
		t.Fatalf("grant A total %d not floor/ceil %d/%d", aggA, fA, cA)
	}
	if !(aggB == fB || aggB == cB) {
		t.Fatalf("grant B total %d not floor/ceil %d/%d", aggB, fB, cB)
	}
	if res.GrantTotals["A"] != aggA || res.GrantTotals["B"] != aggB {
		t.Fatal("GrantTotals inconsistent with lines")
	}
}

func findAlloc(ln LineResult, g string) (Allocation, bool) {
	for _, a := range ln.Allocations {
		if a.GrantID == g {
			return a, true
		}
	}
	return Allocation{}, false
}

func TestZeroAmountLine(t *testing.T) {
	l := mustLedger(t, map[string]int64{"A": 10, "B": 10},
		[]AssetDef{{AssetID: "X", Fundings: []Funding{{"A", 5000}, {"B", 5000}}}})
	res, err := l.Commit("B0", []Expenditure{{LineID: "Z", Asset: "X", Amount: 0}})
	if err != nil {
		t.Fatal(err)
	}
	a := allocOf(t, res.Lines[0], "A")
	b := allocOf(t, res.Lines[0], "B")
	if a != 0 || b != 0 {
		t.Fatalf("zero line alloc = %d,%d", a, b)
	}
	if l.Balance("A") != 10 || l.Balance("B") != 10 {
		t.Fatal("zero line changed balances")
	}
}

// 提交失败后，再提交一个合法批次必须正常；历史中不存在失败批次。
func TestFailedCommitLeavesUsableLedger(t *testing.T) {
	l := mustLedger(t, map[string]int64{"A": 0, "B": 1},
		[]AssetDef{{AssetID: "X", Fundings: []Funding{{"A", 5000}, {"B", 5000}}}})
	_, err := l.Commit("BAD", []Expenditure{{LineID: "L1", Asset: "X", Amount: 2}})
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v", err)
	}
	if _, err := l.Commit("BAD2", []Expenditure{{LineID: "L9", Asset: "X", Amount: 2}}); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("second batch also infeasible, got %v", err)
	}
	if len(l.History()) != 0 {
		t.Fatalf("history len = %d", len(l.History()))
	}
	// 1分钱理论A0.5/B0.5，B有1元可行：记给B
	res, err := l.Commit("OK", []Expenditure{{LineID: "L2", Asset: "X", Amount: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if allocOf(t, res.Lines[0], "B") != 1 {
		t.Fatal("expected 1 to B")
	}
}

func TestValidationErrors(t *testing.T) {
	if _, err := NewLedger(map[string]int64{"A": -1}, nil); err == nil {
		t.Fatal("negative balance accepted")
	}
	if _, err := NewLedger(map[string]int64{"A": 1},
		[]AssetDef{{AssetID: "X", Fundings: []Funding{{"A", 9999}}}}); err == nil {
		t.Fatal("ratios not summing to 10000 accepted")
	}
	if _, err := NewLedger(map[string]int64{"A": 1},
		[]AssetDef{{AssetID: "X", Fundings: []Funding{{"A", 0}, {"B", 10000}}}}); err == nil {
		t.Fatal("zero ratio / unknown grant accepted")
	}
	l := mustLedger(t, map[string]int64{"A": 10},
		[]AssetDef{{AssetID: "X", Fundings: []Funding{{"A", 10000}}}})
	if _, err := l.Trial("", []Expenditure{{LineID: "L", Asset: "X", Amount: 1}}); err == nil {
		t.Fatal("empty batch id accepted")
	}
	if _, err := l.Trial("T", nil); err == nil {
		t.Fatal("empty batch accepted")
	}
	if _, err := l.Trial("T", []Expenditure{{LineID: "L", Asset: "NOPE", Amount: 1}}); err == nil {
		t.Fatal("unknown asset accepted")
	}
	if _, err := l.Commit("DUP", []Expenditure{{LineID: "L", Asset: "X", Amount: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Commit("DUP", []Expenditure{{LineID: "L2", Asset: "X", Amount: 1}}); err == nil {
		t.Fatal("duplicate batch id accepted")
	}
}
