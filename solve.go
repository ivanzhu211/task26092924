package apportion

import "fmt"

// 理论份额的分母：比例以万分比表示。
const ratioBase int64 = 10000

// solver 持有账本快照（拨款余额、资产资助比例），负责整批试算/提交的核心求解。
// solve 为纯函数，不修改 solver 的任何状态。
type solver struct {
	balances map[string]int64   // 拨款 -> 当前余额（分）
	funding  map[string][]Share // 资产 -> 资助拨款及比例
}

// fracEdge 记录一条"支出 -> 拨款"的取整选择边，用于从流中还原方案。
type fracEdge struct {
	expIdx   int
	grantIdx int // 在 grantIDs 中的下标
	node     int // 行节点编号
	edgeIdx  int // 边在流网络中的下标
	floor    int64
}

// solve 对一批支出求最优合法分摊方案；无解时返回 ErrInfeasible。
//
// 建模：设理论份额 t = amount*ratio/10000，实际分配 x ∈ {⌊t⌋, ⌈t⌉}。
// 令 y = x - ⌊t⌋ ∈ {0,1}，则每笔支出行需要恰好 r_e 个上取整；
// 每项拨款列的上取整个数须使列总额 ∈ {⌊Σt⌋, ⌈Σt⌉} 且不超过余额。
// 目标 min Σ|x-t| 等价于 min Σ y·(1-2·frac(t))，即最小费用流。
func (s *solver) solve(batch []Expense) (*Plan, error) {
	if err := s.validate(batch); err != nil {
		return nil, err
	}
	n := len(batch)
	if n == 0 {
		return &Plan{Allocations: nil, GrantTotals: map[string]int64{}}, nil
	}

	// 本批涉及的拨款列。
	grantIdx := map[string]int{}
	var grantIDs []string
	colOf := func(id string) int {
		if c, ok := grantIdx[id]; ok {
			return c
		}
		grantIDs = append(grantIDs, id)
		grantIdx[id] = len(grantIDs) - 1
		return len(grantIDs) - 1
	}

	// 逐笔计算理论份额的向下取整与余数。
	floors := make([][]int64, n) // [exp][share] 向下取整
	rems := make([][]int64, n)   // [exp][share] 分子余数 (0..9999)
	rowNeed := make([]int64, n)  // 每行所需上取整个数 r_e
	colFloor := map[int]int64{}  // 列下标 -> Σ⌊t⌋
	colNum := map[int]int64{}    // 列下标 -> Σ(amount*ratio)
	for i, exp := range batch {
		shares := s.funding[exp.AssetID]
		floors[i] = make([]int64, len(shares))
		rems[i] = make([]int64, len(shares))
		var floorSum int64
		for j, sh := range shares {
			prod := exp.Amount * sh.Ratio
			f, r := prod/ratioBase, prod%ratioBase
			floors[i][j], rems[i][j] = f, r
			floorSum += f
			c := colOf(sh.GrantID)
			colFloor[c] += f
			colNum[c] += prod
		}
		rowNeed[i] = exp.Amount - floorSum
	}

	// 列的上下取整约束，受余额限制。
	m := len(grantIDs)
	colLo := make([]int64, m) // 列必须达到的上取整个数
	colHi := make([]int64, m) // 列允许达到的上取整个数
	for c := 0; c < m; c++ {
		fsum := colFloor[c]
		tnum := colNum[c]
		lo := tnum / ratioBase
		hi := lo
		if tnum%ratioBase != 0 {
			hi = lo + 1
		}
		lo -= fsum
		hi -= fsum
		if room := s.balances[grantIDs[c]] - fsum; hi > room {
			hi = room // 余额不足时列总额只能取下取整
		}
		if hi < lo {
			return nil, ErrInfeasible // 连列的下取整都超过余额
		}
		colLo[c], colHi[c] = lo, hi
	}

	// 组装最小费用流网络。
	// 节点：0=S, 1=T, 2=SS, 3=TT, 4..4+n-1=行, 之后为列。
	const (
		nodeS  = 0
		nodeT  = 1
		nodeSS = 2
		nodeTT = 3
		rowOff = 4
	)
	colOff := rowOff + n
	net := newMCFGraph(colOff + m)
	imb := make([]int64, colOff+m) // 下界预处理产生的节点不平衡量
	var fracs []fracEdge
	for i, exp := range batch {
		row := rowOff + i
		imb[nodeS] += rowNeed[i] // S->行 下界=容量=r_e
		imb[row] -= rowNeed[i]
		for j, sh := range s.funding[exp.AssetID] {
			if rems[i][j] == 0 {
				continue // 理论份额为整数，分配额被唯一确定
			}
			c := grantIdx[sh.GrantID]
			idx := net.addEdge(row, colOff+c, 1, ratioBase-2*rems[i][j])
			fracs = append(fracs, fracEdge{expIdx: i, grantIdx: c, node: row, edgeIdx: idx, floor: floors[i][j]})
		}
	}
	for c := 0; c < m; c++ {
		imb[colOff+c] += colLo[c] // 列->T 下界=colLo
		imb[nodeT] -= colLo[c]
		net.addEdge(colOff+c, nodeT, colHi[c]-colLo[c], 0)
	}
	const infCap = int64(1) << 60
	net.addEdge(nodeT, nodeS, infCap, 0) // 使原网络成为循环流
	var required int64
	for v, b := range imb {
		if b > 0 {
			net.addEdge(v, nodeTT, b, 0)
			required += b
		} else if b < 0 {
			net.addEdge(nodeSS, v, -b, 0)
		}
	}
	flow, _ := net.minCostMaxFlow(nodeSS, nodeTT)
	if flow < required {
		return nil, ErrInfeasible
	}

	// 从流中还原方案：x = ⌊t⌋ + y。
	alloc := make([]map[int]int64, n) // [exp][col] -> 金额
	for i := range alloc {
		alloc[i] = map[int]int64{}
	}
	for i, exp := range batch {
		for j, sh := range s.funding[exp.AssetID] {
			if rems[i][j] == 0 {
				alloc[i][grantIdx[sh.GrantID]] += floors[i][j]
			}
		}
	}
	for _, fe := range fracs {
		y := net.flowOn(fe.node, fe.edgeIdx)
		alloc[fe.expIdx][fe.grantIdx] += fe.floor + y
	}
	plan := &Plan{GrantTotals: map[string]int64{}}
	for i, exp := range batch {
		var sum int64
		for c, amt := range alloc[i] {
			sum += amt
			if amt == 0 {
				continue
			}
			plan.Allocations = append(plan.Allocations, Allocation{
				ExpenseID: exp.ID,
				GrantID:   grantIDs[c],
				Amount:    amt,
			})
			plan.GrantTotals[grantIDs[c]] += amt
		}
		if sum != exp.Amount { // 守恒自检，正常不会触发
			return nil, fmt.Errorf("apportion: internal error: expense %s allocated %d of %d", exp.ID, sum, exp.Amount)
		}
	}
	return plan, nil
}

// validate 校验批次输入的合法性（与余额无关的部分）。
func (s *solver) validate(batch []Expense) error {
	seen := make(map[string]bool, len(batch))
	for _, exp := range batch {
		if exp.ID == "" {
			return fmt.Errorf("apportion: expense with empty ID")
		}
		if seen[exp.ID] {
			return fmt.Errorf("apportion: duplicate expense ID %q", exp.ID)
		}
		seen[exp.ID] = true
		if exp.Amount < 0 {
			return fmt.Errorf("apportion: expense %q has negative amount %d", exp.ID, exp.Amount)
		}
		if _, ok := s.funding[exp.AssetID]; !ok {
			return fmt.Errorf("apportion: unknown asset %q", exp.AssetID)
		}
	}
	return nil
}
