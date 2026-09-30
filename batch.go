package grantalloc

const (
	basisPoints = 10000
	// penaltyBias lexically guarantees every grant's mandatory ("base")
	// up-round slot is filled before any optional slot, since a line
	// deviation is always strictly below 1 cent.
	penaltyBias int64 = 1_000_000
)

// lineItem is one grant candidate of one expenditure line.
type lineItem struct {
	lineIndex int
	grantIdx  int
	floor     int64
	// fraction = (amount*ratio) mod 10000 in [0,10000); up-round cost = 1-f/10000
	fraction int64
}

// grantDemand records aggregate theoretical totals for one grant in a batch.
type grantDemand struct {
	id    string
	floor int64
	// fracSum is the sum of fractions (units of 1/10000) across all items
	fracSum int64
	baseUp  int // mandatory number of up-rounds
	optUp   int // 1 if fracSum mod 10000 != 0, else 0
	balance int64
}

// solverInput is the normalized batch ready for the flow solver.
type solverInput struct {
	lines   []Expenditure
	demands []grantDemand
	items   [][]lineItem // items[lineIndex] -> candidates
}

// plan is a selected allocation plan: ups[lineIndex][itemIndex] is true when
// that item takes ceil instead of floor.
type plan struct {
	ups [][]bool
}

// grantIndex maps grant IDs to compact indices.
type grantIndex struct {
	ids []string
	idx map[string]int
}

func (gi *grantIndex) get(id string) int {
	if i, ok := gi.idx[id]; ok {
		return i
	}
	i := len(gi.ids)
	gi.ids = append(gi.ids, id)
	gi.idx[id] = i
	return i
}

// validateAndNormalize validates a batch and builds solverInput from current
// balances and asset funding definitions.
func (l *Ledger) validateAndNormalize(expenditures []Expenditure) (*solverInput, error) {
	if len(expenditures) == 0 {
		return nil, &ValidationError{Msg: "batch contains no expenditures"}
	}
	seenLines := make(map[string]bool, len(expenditures))
	for _, e := range expenditures {
		if e.LineID == "" {
			return nil, &ValidationError{Msg: "expenditure has empty LineID"}
		}
		if seenLines[e.LineID] {
			return nil, &ValidationError{Msg: "duplicate LineID: " + e.LineID}
		}
		seenLines[e.LineID] = true
		if e.Amount < 0 {
			return nil, &ValidationError{Msg: "negative expenditure amount: " + e.LineID}
		}
		if _, ok := l.assets[e.Asset]; !ok {
			return nil, &ValidationError{Msg: "unknown asset: " + e.Asset}
		}
	}

	gi := &grantIndex{idx: map[string]int{}}
	for id := range l.balances {
		gi.get(id)
	}

	in := &solverInput{
		lines:   expenditures,
		demands: make([]grantDemand, 0, len(gi.ids)),
		items:   make([][]lineItem, len(expenditures)),
	}
	demandByGrant := make(map[string]int, len(gi.ids))
	for _, id := range gi.ids {
		demandByGrant[id] = len(in.demands)
		in.demands = append(in.demands, grantDemand{id: id, balance: l.balances[id]})
	}

	for li, e := range expenditures {
		fundings := l.assets[e.Asset]
		items := make([]lineItem, 0, len(fundings))
		for _, fd := range fundings {
			dg := demandByGrant[fd.GrantID]
			prod := e.Amount * int64(fd.Ratio)
			fl := prod / basisPoints
			frac := prod - fl*basisPoints
			items = append(items, lineItem{lineIndex: li, grantIdx: dg, floor: fl, fraction: frac})
			d := &in.demands[dg]
			d.floor += fl
			d.fracSum += frac
		}
		in.items[li] = items
	}

	for dg := range in.demands {
		d := &in.demands[dg]
		d.baseUp = int(d.fracSum / basisPoints)
		if d.fracSum%basisPoints != 0 {
			d.optUp = 1
		}
	}
	return in, nil
}

type edgeRef struct {
	from, idx int
	grant     int
	itemPos   int
	capacity  int
	base      bool
}

// solvePlan finds the minimum-total-deviation feasible plan.
// Returns ErrInfeasible when no feasible plan exists.
func solvePlan(in *solverInput) (*plan, error) {
	nLines := len(in.lines)
	nGrants := len(in.demands)

	// Nodes: 0 source, line nodes, grant nodes, sink (edges added in this
	// topo order, which the flow solver relies on for initial potentials).
	src := 0
	lineBase := 1
	grantBase := lineBase + nLines
	sink := grantBase + nGrants
	g := newMinCostFlow(sink + 1)

	// source -> line: capacity = number of ceil-rounds on the line.
	lineCap := make([]int, nLines)
	for li, items := range in.items {
		var fracSum int64
		for _, it := range items {
			fracSum += it.fraction
		}
		lineCap[li] = int(fracSum / basisPoints)
		if lineCap[li] > 0 {
			g.addEdge(src, lineBase+li, lineCap[li], 0)
		}
	}

	// line -> grant: one unit rounds that item up. Items with zero fractional
	// part cannot be rounded up, so they get no edge.
	lineEdges := make([][]edgeRef, nLines)
	for li, items := range in.items {
		for pos, it := range items {
			if it.fraction == 0 {
				continue
			}
			cost := basisPoints - it.fraction // up-round deviation in 1/10000
			from, idx := g.addEdge(lineBase+li, grantBase+it.grantIdx, 1, cost)
			lineEdges[li] = append(lineEdges[li], edgeRef{
				from: from, idx: idx, grant: it.grantIdx, itemPos: pos,
			})
		}
	}

	// grant -> sink: mandatory base slots (negative-cost) plus at most one
	// optional slot. Total slots are capped by available balance.
	grantEdges := make([]edgeRef, 0, 2*nGrants)
	for dg, d := range in.demands {
		room := d.balance - d.floor
		if room < 0 {
			return nil, ErrInfeasible
		}
		base := d.baseUp
		if base > int(room) {
			return nil, ErrInfeasible
		}
		if base > 0 {
			from, idx := g.addEdge(grantBase+dg, sink, base, -penaltyBias)
			grantEdges = append(grantEdges, edgeRef{from: from, idx: idx, capacity: base, base: true})
		}
		if d.optUp == 1 && room > int64(base) {
			from, idx := g.addEdge(grantBase+dg, sink, 1, 0)
			grantEdges = append(grantEdges, edgeRef{from: from, idx: idx, capacity: 1, base: false})
		}
	}

	totalFlow := 0
	for _, c := range lineCap {
		totalFlow += c
	}
	if totalFlow > 0 {
		if fl, _ := g.flow(src, sink, totalFlow); fl != totalFlow {
			return nil, ErrInfeasible
		}
		// Every mandatory base slot must be saturated; otherwise some grant
		// total would fall below floor(sum of its theoretical shares).
		for _, ge := range grantEdges {
			if ge.base && g.edgeFlow(ge.from, ge.idx) != ge.capacity {
				return nil, ErrInfeasible
			}
		}
	}

	p := &plan{ups: make([][]bool, nLines)}
	for li, items := range in.items {
		p.ups[li] = make([]bool, len(items))
		for _, r := range lineEdges[li] {
			if g.edgeFlow(r.from, r.idx) > 0 {
				p.ups[li][r.itemPos] = true
			}
		}
	}
	return p, nil
}

// buildResult converts a plan into a BatchResult with exact total deviation.
func (in *solverInput) buildResult(p *plan) *BatchResult {
	lines := make([]LineResult, len(in.lines))
	totals := make(map[string]int64, len(in.demands))
	var devNum int64 // deviation numerator in units of 1/10000 cent
	for li, e := range in.lines {
		allocs := make([]Allocation, 0, len(in.items[li]))
		for pos, it := range in.items[li] {
			amt := it.floor
			if p.ups[li][pos] {
				amt++
			}
			allocs = append(allocs, Allocation{GrantID: in.demands[it.grantIdx].id, Amount: amt})
			totals[in.demands[it.grantIdx].id] += amt
			if p.ups[li][pos] {
				devNum += basisPoints - it.fraction
			} else {
				devNum += it.fraction
			}
		}
		lines[li] = LineResult{LineID: e.LineID, Asset: e.Asset, Amount: e.Amount, Allocations: allocs}
	}
	return &BatchResult{
		Lines:          lines,
		GrantTotals:    totals,
		TotalDeviation: float64(devNum) / basisPoints,
	}
}
