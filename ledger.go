// Package grantalloc implements batch allocation of asset maintenance
// expenditures across multiple funding grants.
//
// All amounts are integer cents. Every allocation per expenditure is either
// floor(amount*p/10000) or ceil(amount*p/10000); grant totals are likewise
// floor/ceil of the sum of theoretical shares, never exceed current balances,
// and the selected feasible plan minimizes the total absolute rounding
// deviation across every line allocation.
package grantalloc

import (
	"errors"
)

// Funding is one grant's fixed funding ratio (basis points) for an asset.
type Funding struct {
	GrantID string
	Ratio   int // basis points; ratios for one asset sum to exactly 10000
}

// AssetDef defines the grants funding one asset and their fixed ratios.
type AssetDef struct {
	AssetID  string
	Fundings []Funding
}

// Expenditure is one expenditure line inside a batch.
type Expenditure struct {
	LineID string
	Asset  string
	Amount int64 // integer cents, non-negative
}

// Allocation is one grant's actual allocation for one expenditure line.
type Allocation struct {
	GrantID string
	Amount  int64
}

// LineResult is one expenditure line and its independent allocations.
type LineResult struct {
	LineID      string
	Asset       string
	Amount      int64
	Allocations []Allocation
}

// BatchResult is the result of a trial or a successful commit.
type BatchResult struct {
	Lines          []LineResult
	GrantTotals    map[string]int64
	TotalDeviation float64 // exact sum of |actual - theoretical| over all items
}

// CommittedBatch is a batch that has been posted to the ledger.
type CommittedBatch struct {
	BatchID string
	Result  BatchResult
}

// ErrInfeasible is returned when no allocation plan satisfies the rules.
var ErrInfeasible = errors.New("grantalloc: batch is not allocatable under current balances")

// ValidationError describes a malformed input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "grantalloc: " + e.Msg }

// Ledger keeps grant balances, asset funding ratios and posted history.
// Methods are intended for serialized (single-goroutine) use.
type Ledger struct {
	balances map[string]int64
	assets   map[string][]Funding
	history  []CommittedBatch
}

// NewLedger creates a Ledger with initial grant balances and asset definitions.
func NewLedger(initialBalances map[string]int64, assets []AssetDef) (*Ledger, error) {
	if len(initialBalances) == 0 {
		return nil, &ValidationError{Msg: "no grant balances provided"}
	}
	balances := make(map[string]int64, len(initialBalances))
	for id, b := range initialBalances {
		if id == "" {
			return nil, &ValidationError{Msg: "grant id is empty"}
		}
		if b < 0 {
			return nil, &ValidationError{Msg: "negative initial balance: " + id}
		}
		balances[id] = b
	}
	assetMap := make(map[string][]Funding, len(assets))
	for _, a := range assets {
		if a.AssetID == "" {
			return nil, &ValidationError{Msg: "asset id is empty"}
		}
		if _, dup := assetMap[a.AssetID]; dup {
			return nil, &ValidationError{Msg: "duplicate asset: " + a.AssetID}
		}
		if len(a.Fundings) == 0 {
			return nil, &ValidationError{Msg: "asset has no funding: " + a.AssetID}
		}
		sum := 0
		seen := make(map[string]bool, len(a.Fundings))
		fundings := make([]Funding, len(a.Fundings))
		for i, fd := range a.Fundings {
			if fd.GrantID == "" {
				return nil, &ValidationError{Msg: "grant id is empty for asset " + a.AssetID}
			}
			if seen[fd.GrantID] {
				return nil, &ValidationError{Msg: "duplicate grant on asset: " + a.AssetID + "/" + fd.GrantID}
			}
			seen[fd.GrantID] = true
			if fd.Ratio <= 0 {
				return nil, &ValidationError{Msg: "non-positive ratio: " + a.AssetID + "/" + fd.GrantID}
			}
			bal, ok := balances[fd.GrantID]
			if !ok {
				return nil, &ValidationError{Msg: "unknown grant funding asset: " + a.AssetID + "/" + fd.GrantID}
			}
			_ = bal
			sum += fd.Ratio
			fundings[i] = fd
		}
		if sum != basisPoints {
			return nil, &ValidationError{Msg: "funding ratios do not sum to 10000 for asset: " + a.AssetID}
		}
		assetMap[a.AssetID] = fundings
	}
	return &Ledger{balances: balances, assets: assetMap}, nil
}

// Balance returns the current balance of a grant (0 if unknown).
func (l *Ledger) Balance(grantID string) int64 {
	return l.balances[grantID]
}

// Balances returns a copy of all current grant balances.
func (l *Ledger) Balances() map[string]int64 {
	out := make(map[string]int64, len(l.balances))
	for k, v := range l.balances {
		out[k] = v
	}
	return out
}

// History returns a deep copy of all committed batches in commit order.
func (l *Ledger) History() []CommittedBatch {
	out := make([]CommittedBatch, len(l.history))
	for i, cb := range l.history {
		out[i] = cloneCommittedBatch(cb)
	}
	return out
}

// Trial computes the optimal feasible plan for a batch without mutating state.
func (l *Ledger) Trial(batchID string, expenditures []Expenditure) (*BatchResult, error) {
	if batchID == "" {
		return nil, &ValidationError{Msg: "batch id is empty"}
	}
	in, err := l.validateAndNormalize(expenditures)
	if err != nil {
		return nil, err
	}
	p, err := solvePlan(in)
	if err != nil {
		return nil, err
	}
	return cloneResult(in.buildResult(p)), nil
}

// Commit computes the plan against current balances and posts it atomically.
// On any error (including ErrInfeasible) no balance or history change occurs.
func (l *Ledger) Commit(batchID string, expenditures []Expenditure) (*BatchResult, error) {
	if batchID == "" {
		return nil, &ValidationError{Msg: "batch id is empty"}
	}
	for _, cb := range l.history {
		if cb.BatchID == batchID {
			return nil, &ValidationError{Msg: "batch already committed: " + batchID}
		}
	}
	in, err := l.validateAndNormalize(expenditures)
	if err != nil {
		return nil, err
	}
	p, err := solvePlan(in)
	if err != nil {
		return nil, err
	}
	res := in.buildResult(p)

	// Deduct balances, asserting they never go negative (the solver already
	// guarantees this); keep the check as an invariant guard.
	newBalances := l.Balances()
	for gid, total := range res.GrantTotals {
		if total > newBalances[gid] {
			return nil, ErrInfeasible
		}
		newBalances[gid] -= total
	}
	l.balances = newBalances
	l.history = append(l.history, CommittedBatch{BatchID: batchID, Result: *cloneResult(res)})
	return cloneResult(res), nil
}

func cloneResult(r *BatchResult) *BatchResult {
	c := &BatchResult{
		Lines:          make([]LineResult, len(r.Lines)),
		GrantTotals:    make(map[string]int64, len(r.GrantTotals)),
		TotalDeviation: r.TotalDeviation,
	}
	for i, ln := range r.Lines {
		c.Lines[i] = LineResult{
			LineID:      ln.LineID,
			Asset:       ln.Asset,
			Amount:      ln.Amount,
			Allocations: append([]Allocation(nil), ln.Allocations...),
		}
	}
	for k, v := range r.GrantTotals {
		c.GrantTotals[k] = v
	}
	return c
}

func cloneCommittedBatch(cb CommittedBatch) CommittedBatch {
	return CommittedBatch{BatchID: cb.BatchID, Result: *cloneResult(&cb.Result)}
}
