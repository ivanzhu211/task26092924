package grantalloc_test

import (
	"fmt"

	"grantalloc"
)

// Example shows a two-line batch where greedy per-line rounding would
// overspend grant A; the batch solver finds the globally feasible plan.
func Example() {
	ledger, err := grantalloc.NewLedger(
		map[string]int64{"A": 1, "B": 1},
		[]grantalloc.AssetDef{
			{AssetID: "X", Fundings: []grantalloc.Funding{{GrantID: "A", Ratio: 6000}, {GrantID: "B", Ratio: 4000}}},
			{AssetID: "Y", Fundings: []grantalloc.Funding{{GrantID: "A", Ratio: 7000}, {GrantID: "B", Ratio: 3000}}},
		})
	if err != nil {
		panic(err)
	}

	// Trial only computes; balances and history are untouched.
	plan, err := ledger.Trial("trial-1", []grantalloc.Expenditure{
		{LineID: "L1", Asset: "X", Amount: 1},
		{LineID: "L2", Asset: "Y", Amount: 1},
	})
	if err != nil {
		panic(err)
	}
	for _, line := range plan.Lines {
		fmt.Printf("%s:", line.LineID)
		for _, a := range line.Allocations {
			fmt.Printf(" %s=%d", a.GrantID, a.Amount)
		}
		fmt.Println()
	}
	fmt.Printf("deviation=%.1f A-balance-after-trial=%d\n", plan.TotalDeviation, ledger.Balance("A"))

	if _, err := ledger.Commit("batch-1", []grantalloc.Expenditure{
		{LineID: "L1", Asset: "X", Amount: 1},
		{LineID: "L2", Asset: "Y", Amount: 1},
	}); err != nil {
		panic(err)
	}
	fmt.Printf("after commit: A=%d B=%d history=%d\n",
		ledger.Balance("A"), ledger.Balance("B"), len(ledger.History()))
	// Output:
	// L1: A=0 B=1
	// L2: A=1 B=0
	// deviation=1.8 A-balance-after-trial=1
	// after commit: A=0 B=0 history=1
}
