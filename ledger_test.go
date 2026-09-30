package apportion

import (
	"errors"
	"math/rand"
	"testing"
)

// allocOf 在方案中查找某笔支出分到某拨款的金额（无记录返回 0）。
func allocOf(p *Plan, expenseID, grantID string) int64 {
	for _, a := range p.Allocations {
		if a.ExpenseID == expenseID && a.GrantID == grantID {
			return a.Amount
		}
	}
	return 0
}

// 验收场景 1：两笔支出各 1 分，首笔 A60/B40，次笔仅 A；A、B 余额各 1 分。
// 必须首笔全部记给 B、次笔全部记给 A。
func TestScenarioDeferredRounding(t *testing.T) {
	l, err := NewLedger(
		map[string]int64{"A": 1, "B": 1},
		map[string][]Share{
			"asset1": {{"A", 6000}, {"B", 4000}},
			"asset2": {{"A", 10000}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	batch := []Expense{
		{ID: "e1", AssetID: "asset1", Amount: 1},
		{ID: "e2", AssetID: "asset2", Amount: 1},
	}
	// 先试算：不得改变账目。
	if _, err := l.Trial(batch); err != nil {
		t.Fatalf("trial: %v", err)
	}
	if l.Balance("A") != 1 || l.Balance("B") != 1 || len(l.Records()) != 0 {
		t.Fatalf("trial mutated state: A=%d B=%d records=%d", l.Balance("A"), l.Balance("B"), len(l.Records()))
	}
	plan, err := l.Submit(batch)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := allocOf(plan, "e1", "B"); got != 1 {
		t.Errorf("e1->B = %d, want 1", got)
	}
	if got := allocOf(plan, "e1", "A"); got != 0 {
		t.Errorf("e1->A = %d, want 0", got)
	}
	if got := allocOf(plan, "e2", "A"); got != 1 {
		t.Errorf("e2->A = %d, want 1", got)
	}
	if l.Balance("A") != 0 || l.Balance("B") != 0 {
		t.Errorf("balances A=%d B=%d, want 0/0", l.Balance("A"), l.Balance("B"))
	}
	if len(l.Records()) != 2 {
		t.Errorf("records = %d, want 2", len(l.Records()))
	}
}

// 验收场景 2：两笔各 1 分，首笔 A60/B40，次笔 A70/B30，余额各 1 分。
// 最优方案：首笔给 B、次笔给 A，逐笔绝对偏差总和恰为 1.8 分。
func TestScenarioOptimalDeviation(t *testing.T) {
	l, err := NewLedger(
		map[string]int64{"A": 1, "B": 1},
		map[string][]Share{
			"asset1": {{"A", 6000}, {"B", 4000}},
			"asset2": {{"A", 7000}, {"B", 3000}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	batch := []Expense{
		{ID: "e1", AssetID: "asset1", Amount: 1},
		{ID: "e2", AssetID: "asset2", Amount: 1},
	}
	plan, err := l.Submit(batch)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got := allocOf(plan, "e1", "B"); got != 1 {
		t.Errorf("e1->B = %d, want 1 (optimal plan)", got)
	}
	if got := allocOf(plan, "e2", "A"); got != 1 {
		t.Errorf("e2->A = %d, want 1 (optimal plan)", got)
	}
	// 精确偏差：Σ|x·10000 - amount·ratio| / 10000。
	dev := scaledDeviation(t, l, batch, plan)
	if dev != 18000 {
		t.Errorf("scaled deviation = %d, want 18000 (1.8 分)", dev)
	}
}

// scaledDeviation 以 1/10000 分为单位计算方案的逐笔绝对偏差总和。
func scaledDeviation(t *testing.T, l *Ledger, batch []Expense, p *Plan) int64 {
	t.Helper()
	var total int64
	for _, exp := range batch {
		for _, sh := range l.solver.funding[exp.AssetID] {
			x := allocOf(p, exp.ID, sh.GrantID) * ratioBase
			theory := exp.Amount * sh.Ratio
			d := x - theory
			if d < 0 {
				d = -d
			}
			total += d
		}
	}
	return total
}

// 验收场景 3：余额总和足够，但资助关系限制使整批无法分摊；
// 拒绝前后余额与历史记录必须完全相同。
func TestScenarioInfeasibleByFunding(t *testing.T) {
	l, err := NewLedger(
		map[string]int64{"A": 3, "B": 100},
		map[string][]Share{
			"asset1": {{"A", 10000}}, // 仅 A 资助，B 不能承接
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	batch := []Expense{{ID: "e1", AssetID: "asset1", Amount: 5}}
	if _, err := l.Trial(batch); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("trial err = %v, want ErrInfeasible", err)
	}
	if _, err := l.Submit(batch); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("submit err = %v, want ErrInfeasible", err)
	}
	if l.Balance("A") != 3 || l.Balance("B") != 100 {
		t.Errorf("balances changed after rejection: A=%d B=%d", l.Balance("A"), l.Balance("B"))
	}
	if len(l.Records()) != 0 {
		t.Errorf("records changed after rejection: %d", len(l.Records()))
	}
}

// 整批不可分摊的另一种形态：列下取整即超余额。
func TestInfeasibleByBalance(t *testing.T) {
	l, err := NewLedger(
		map[string]int64{"A": 1},
		map[string][]Share{"asset1": {{"A", 10000}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = l.Submit([]Expense{{ID: "e1", AssetID: "asset1", Amount: 2}})
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
	if l.Balance("A") != 1 {
		t.Errorf("balance = %d, want 1", l.Balance("A"))
	}
}

// 提交后余额可支撑后续批次，且第二次提交按当时余额重新求解。
func TestSequentialBatches(t *testing.T) {
	l, err := NewLedger(
		map[string]int64{"A": 2, "B": 1},
		map[string][]Share{"asset1": {{"A", 5000}, {"B", 5000}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Submit([]Expense{{ID: "e1", AssetID: "asset1", Amount: 2}}); err != nil {
		t.Fatal(err)
	}
	// A、B 各扣 1（理论份额恰为整数）。
	if l.Balance("A") != 1 || l.Balance("B") != 0 {
		t.Fatalf("balances A=%d B=%d, want 1/0", l.Balance("A"), l.Balance("B"))
	}
	// 第二批 2 分：B 已无余款，理论份额 1/1，B 列下取整为 1 > 余额 0 -> 不可分摊。
	if _, err := l.Submit([]Expense{{ID: "e2", AssetID: "asset1", Amount: 2}}); !errors.Is(err, ErrInfeasible) {
		t.Fatalf("err = %v, want ErrInfeasible", err)
	}
	if len(l.Records()) != 2 {
		t.Errorf("records = %d, want 2 (only first batch)", len(l.Records()))
	}
}

// 输入校验：比例和不为 10000、未知资产、重复支出号。
func TestValidation(t *testing.T) {
	if _, err := NewLedger(map[string]int64{"A": 1}, map[string][]Share{
		"asset1": {{"A", 9999}},
	}); err == nil {
		t.Error("want error for ratio sum != 10000")
	}
	l, err := NewLedger(map[string]int64{"A": 10}, map[string][]Share{
		"asset1": {{"A", 10000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Submit([]Expense{{ID: "e1", AssetID: "nope", Amount: 1}}); err == nil {
		t.Error("want error for unknown asset")
	}
	dup := []Expense{
		{ID: "e1", AssetID: "asset1", Amount: 1},
		{ID: "e1", AssetID: "asset1", Amount: 1},
	}
	if _, err := l.Submit(dup); err == nil {
		t.Error("want error for duplicate expense ID")
	}
	if l.Balance("A") != 10 || len(l.Records()) != 0 {
		t.Error("validation failure must not change state")
	}
}

// 通用守恒检查：方案满足全部业务约束。
func checkPlanConservation(t *testing.T, l *Ledger, batch []Expense, p *Plan, balances map[string]int64) {
	t.Helper()
	perExp := map[string]int64{}
	perGrant := map[string]int64{}
	for _, a := range p.Allocations {
		perExp[a.ExpenseID] += a.Amount
		perGrant[a.GrantID] += a.Amount
	}
	theoryNum := map[string]int64{} // 拨款 -> Σ(amount*ratio)
	for _, exp := range batch {
		if perExp[exp.ID] != exp.Amount {
			t.Errorf("expense %s allocated %d of %d", exp.ID, perExp[exp.ID], exp.Amount)
		}
		for _, sh := range l.solver.funding[exp.AssetID] {
			x := allocOf(p, exp.ID, sh.GrantID)
			lo := exp.Amount * sh.Ratio / ratioBase
			if x != lo && x != lo+1 {
				t.Errorf("expense %s grant %s: x=%d not in {%d,%d}", exp.ID, sh.GrantID, x, lo, lo+1)
			}
			theoryNum[sh.GrantID] += exp.Amount * sh.Ratio
		}
	}
	for g, tot := range perGrant {
		lo := theoryNum[g] / ratioBase
		hi := lo
		if theoryNum[g]%ratioBase != 0 {
			hi++
		}
		if tot != lo && tot != hi {
			t.Errorf("grant %s total %d not in {%d,%d}", g, tot, lo, hi)
		}
		if tot > balances[g] {
			t.Errorf("grant %s total %d exceeds balance %d", g, tot, balances[g])
		}
		if p.GrantTotals[g] != tot {
			t.Errorf("grant %s GrantTotals=%d, want %d", g, p.GrantTotals[g], tot)
		}
	}
}

func TestConservationRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		nGrants := 1 + rng.Intn(6)
		balances := map[string]int64{}
		var ids []string
		for i := 0; i < nGrants; i++ {
			id := string(rune('A' + i))
			ids = append(ids, id)
			balances[id] = int64(rng.Intn(50))
		}
		funding := map[string][]Share{}
		var assets []string
		for a := 0; a < 1+rng.Intn(4); a++ {
			asset := string(rune('a' + a))
			assets = append(assets, asset)
			k := 1 + rng.Intn(nGrants)
			perm := rng.Perm(nGrants)[:k]
			shares := randomShares(rng, k)
			for i, gi := range perm {
				funding[asset] = append(funding[asset], Share{ids[gi], shares[i]})
			}
		}
		l, err := NewLedger(balances, funding)
		if err != nil {
			t.Fatal(err)
		}
		var batch []Expense
		for i := 0; i < 1+rng.Intn(8); i++ {
			batch = append(batch, Expense{
				ID:      string(rune('0'+trial)) + "-" + string(rune('0'+i)),
				AssetID: assets[rng.Intn(len(assets))],
				Amount:  int64(rng.Intn(30)),
			})
		}
		plan, err := l.Trial(batch)
		if errors.Is(err, ErrInfeasible) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		checkPlanConservation(t, l, batch, plan, balances)
	}
}

// randomShares 生成 k 个和为 10000 的正万分比。
func randomShares(rng *rand.Rand, k int) []int64 {
	out := make([]int64, k)
	rem := ratioBase
	for i := 0; i < k-1; i++ {
		v := 1 + rng.Int63n(rem-int64(k-i)+1)
		out[i] = v
		rem -= v
	}
	out[k-1] = rem
	return out
}
