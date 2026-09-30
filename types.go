// Package apportion 提供拨款支出分摊能力：对同一报表期内的一批支出，
// 按资产资助比例在多项拨款之间进行整批统一分摊，保证逐笔账、拨款汇总账
// 与剩余额度三者一致。
package apportion

import "errors"

// ErrInfeasible 表示整批支出不存在同时满足全部约束的分摊方案。
var ErrInfeasible = errors.New("apportion: batch cannot be allocated")

// Share 描述一项拨款对某资产的资助比例，Ratio 为万分比整数。
type Share struct {
	GrantID string
	Ratio   int64
}

// Expense 是批次中的一笔支出，金额单位为分。
type Expense struct {
	ID      string
	AssetID string
	Amount  int64
}

// Allocation 是一笔支出分摊到一项拨款的明细结果。
type Allocation struct {
	ExpenseID string
	GrantID   string
	Amount    int64
}

// Plan 是整批分摊方案：逐笔明细 + 各拨款本批分摊总额。
type Plan struct {
	Allocations []Allocation
	GrantTotals map[string]int64
}

// Record 是已记账的分摊明细（含批次序号与提交时间序号）。
type Record struct {
	Batch     int
	ExpenseID string
	AssetID   string
	GrantID   string
	Amount    int64
}
