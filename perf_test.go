package apportion

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"
)

// 满规模批次：2000 笔支出、128 项拨款、每项资产最多 4 项拨款、
// 单笔最高 10 亿分。要求完整试算 10 秒内完成、峰值内存不超过 256 MiB，
// 并检查各项守恒。
func TestFullScaleBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full-scale test in -short mode")
	}
	rng := rand.New(rand.NewSource(7))

	const nGrants = 128
	balances := map[string]int64{}
	grantIDs := make([]string, nGrants)
	for i := 0; i < nGrants; i++ {
		grantIDs[i] = fmt.Sprintf("G%03d", i)
		balances[grantIDs[i]] = 1 << 40 // 余额充足，保证可分摊
	}

	// 1000 项资产，每项 1..4 项拨款。
	funding := map[string][]Share{}
	assets := make([]string, 1000)
	for a := range assets {
		assets[a] = fmt.Sprintf("A%04d", a)
		k := 1 + rng.Intn(4)
		shares := randomShares(rng, k)
		for i, gi := range rng.Perm(nGrants)[:k] {
			funding[assets[a]] = append(funding[assets[a]], Share{grantIDs[gi], shares[i]})
		}
	}

	// 2000 笔支出，金额最高 10 亿分。
	batch := make([]Expense, 2000)
	for i := range batch {
		batch[i] = Expense{
			ID:      fmt.Sprintf("E%04d", i),
			AssetID: assets[rng.Intn(len(assets))],
			Amount:  rng.Int63n(1_000_000_000) + 1,
		}
	}

	l, err := NewLedger(balances, funding)
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	start := time.Now()
	plan, err := l.Trial(batch)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("trial: %v", err)
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	peak := memAfter.HeapInuse + memAfter.StackInuse
	const limit = 256 << 20

	t.Logf("满规模试算: %d 笔支出, %d 项拨款, 耗时 %v", len(batch), nGrants, elapsed)
	t.Logf("堆内存在用 %d MiB (限额 256 MiB), 累计分配 %d MiB",
		peak>>20, (memAfter.TotalAlloc-memBefore.TotalAlloc)>>20)

	if elapsed > 10*time.Second {
		t.Errorf("试算耗时 %v 超过 10 秒", elapsed)
	}
	if peak > limit {
		t.Errorf("峰值内存 %d MiB 超过 256 MiB", peak>>20)
	}

	// 守恒检查：逐笔、逐拨款、余额。
	checkPlanConservation(t, l, batch, plan, balances)

	// 提交后余额恰好扣除本批分配总额，且试算未改变账目。
	if _, err := l.Submit(batch); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for id, want := range balances {
		got := l.Balance(id)
		if got+plan.GrantTotals[id] != want {
			t.Errorf("grant %s: balance %d + allocated %d != initial %d", id, got, plan.GrantTotals[id], want)
		}
	}
	// 已记账明细与本批方案一致。
	recs := l.Records()
	if len(recs) != len(plan.Allocations) {
		t.Fatalf("records %d != allocations %d", len(recs), len(plan.Allocations))
	}
	var recSum int64
	for _, r := range recs {
		recSum += r.Amount
	}
	var planSum int64
	for _, a := range plan.Allocations {
		planSum += a.Amount
	}
	if recSum != planSum {
		t.Errorf("records sum %d != plan sum %d", recSum, planSum)
	}
}
