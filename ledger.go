package apportion

import "fmt"

// Ledger 是拨款分摊账本：保存拨款余额、资产资助比例与已记账分摊明细。
// 仅供单报表期内顺序调用，不做并发保护。
type Ledger struct {
	solver  *solver
	records []Record
	batches int
}

// NewLedger 以给定拨款初始余额与资产资助比例创建账本。
// 比例以万分比整数表示，每项资产的正比例之和必须恰好为 10000；
// 资助关系中的拨款必须出现在 balances 中。
func NewLedger(balances map[string]int64, funding map[string][]Share) (*Ledger, error) {
	bal := make(map[string]int64, len(balances))
	for id, b := range balances {
		if b < 0 {
			return nil, fmt.Errorf("apportion: grant %q has negative balance %d", id, b)
		}
		bal[id] = b
	}
	fund := make(map[string][]Share, len(funding))
	for asset, shares := range funding {
		if len(shares) == 0 {
			return nil, fmt.Errorf("apportion: asset %q has no funding grant", asset)
		}
		var sum int64
		cp := make([]Share, len(shares))
		seen := map[string]bool{}
		for i, sh := range shares {
			if sh.Ratio <= 0 {
				return nil, fmt.Errorf("apportion: asset %q grant %q has non-positive ratio %d", asset, sh.GrantID, sh.Ratio)
			}
			if seen[sh.GrantID] {
				return nil, fmt.Errorf("apportion: asset %q lists grant %q twice", asset, sh.GrantID)
			}
			seen[sh.GrantID] = true
			if _, ok := bal[sh.GrantID]; !ok {
				return nil, fmt.Errorf("apportion: asset %q funded by unknown grant %q", asset, sh.GrantID)
			}
			sum += sh.Ratio
			cp[i] = sh
		}
		if sum != ratioBase {
			return nil, fmt.Errorf("apportion: asset %q ratios sum to %d, want %d", asset, sum, ratioBase)
		}
		fund[asset] = cp
	}
	return &Ledger{solver: &solver{balances: bal, funding: fund}}, nil
}

// Trial 整批试算：返回最优合法方案，不改变任何账目。
func (l *Ledger) Trial(batch []Expense) (*Plan, error) {
	return l.solver.solve(batch)
}

// Submit 整批提交记账：按当前余额重新求解最优方案，
// 成功后各拨款余额恰好扣除本批实际分配总额，并追加分摊明细。
// 返回 ErrInfeasible 时账目保持不变。
func (l *Ledger) Submit(batch []Expense) (*Plan, error) {
	plan, err := l.solver.solve(batch)
	if err != nil {
		return nil, err
	}
	for id, total := range plan.GrantTotals {
		l.solver.balances[id] -= total
	}
	l.batches++
	assetOf := make(map[string]string, len(batch))
	for _, exp := range batch {
		assetOf[exp.ID] = exp.AssetID
	}
	for _, a := range plan.Allocations {
		l.records = append(l.records, Record{
			Batch:     l.batches,
			ExpenseID: a.ExpenseID,
			AssetID:   assetOf[a.ExpenseID],
			GrantID:   a.GrantID,
			Amount:    a.Amount,
		})
	}
	return plan, nil
}

// Balance 返回拨款当前余额（分）。
func (l *Ledger) Balance(grantID string) int64 {
	return l.solver.balances[grantID]
}

// Records 返回全部已记账分摊明细的副本（按记账顺序）。
func (l *Ledger) Records() []Record {
	out := make([]Record, len(l.records))
	copy(out, l.records)
	return out
}
