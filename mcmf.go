package grantalloc

// mincostflow is a sparse directed min-cost max-flow graph with int64 costs.
// Costs on forward edges may be negative; edges must be added from lower to
// higher node index (initial graph is a DAG), so initial potentials are
// computed in one topological relaxation pass.
type mincostflow struct {
	n     int
	graph [][]mcmfEdge
}

type mcmfEdge struct {
	to, rev int
	cap     int
	cost    int64
}

func newMinCostFlow(n int) *mincostflow {
	return &mincostflow{n: n, graph: make([][]mcmfEdge, n)}
}

// addEdge adds a directed edge with capacity cap and unit cost cost.
// It returns (from, index) identifying the forward edge.
func (g *mincostflow) addEdge(from, to, cap int, cost int64) (int, int) {
	idx := len(g.graph[from])
	g.graph[from] = append(g.graph[from], mcmfEdge{to: to, rev: len(g.graph[to]), cap: cap, cost: cost})
	g.graph[to] = append(g.graph[to], mcmfEdge{to: from, rev: idx, cap: 0, cost: -cost})
	return from, idx
}

// edgeFlow returns the current flow carried by a forward edge.
func (g *mincostflow) edgeFlow(from, idx int) int {
	e := g.graph[from][idx]
	rev := g.graph[e.to][e.rev]
	return rev.cap
}

// initPotential computes shortest distances from s over the initial DAG.
// Forward edges are added in topological node order, so one pass in node
// index order relaxes every edge exactly like a DAG shortest path.
func (g *mincostflow) initPotential(s int) []int64 {
	h := make([]int64, g.n)
	for i := range h {
		h[i] = inf64
	}
	h[s] = 0
	for u := 0; u < g.n; u++ {
		if h[u] == inf64 {
			continue
		}
		for _, e := range g.graph[u] {
			if e.cap > 0 && h[u]+e.cost < h[e.to] {
				h[e.to] = h[u] + e.cost
			}
		}
	}
	return h
}

const inf64 = int64(1) << 62

// flow sends up to f units from s to t at minimum total cost, using
// successive shortest augmenting paths with reduced-cost Dijkstra.
// It returns the units actually sent and the total (original) cost.
func (g *mincostflow) flow(s, t, f int) (int, int64) {
	h := g.initPotential(s)
	dist := make([]uint64, g.n)
	prevv := make([]int, g.n)
	preve := make([]int, g.n)
	flow := 0
	var totalCost int64

	rh := newRadixHeap()
	for flow < f {
		for i := range dist {
			dist[i] = radixInf
		}
		dist[s] = 0
		rh.clear()
		rh.push(0, s)
		for rh.len() > 0 {
			key, u := rh.pop()
			if key != dist[u] {
				continue
			}
			for i, e := range g.graph[u] {
				if e.cap == 0 {
					continue
				}
				rc := e.cost + h[u] - h[e.to]
				nd := key + uint64(rc)
				if nd < dist[e.to] {
					dist[e.to] = nd
					prevv[e.to] = u
					preve[e.to] = i
					rh.push(nd, e.to)
				}
			}
		}
		if dist[t] == radixInf {
			break
		}
		for v := 0; v < g.n; v++ {
			if dist[v] < radixInf {
				h[v] += int64(dist[v])
			}
		}
		d := f - flow
		for v := t; v != s; v = prevv[v] {
			if c := g.graph[prevv[v]][preve[v]].cap; c < d {
				d = c
			}
		}
		flow += d
		totalCost += int64(d) * h[t]
		for v := t; v != s; v = prevv[v] {
			e := &g.graph[prevv[v]][preve[v]]
			e.cap -= d
			g.graph[v][e.rev].cap += d
		}
	}
	return flow, totalCost
}

// radixHeap is a monotone min-heap over uint64 keys (Ahuja et al. radix heap).
type radixHeap struct {
	buckets [65][]rhEntry
	mins    [65]uint64
	size    int
	last    uint64
}

type rhEntry struct {
	key  uint64
	node int
}

const radixInf = ^uint64(0)

func newRadixHeap() *radixHeap {
	h := &radixHeap{last: 0}
	for i := range h.mins {
		h.mins[i] = radixInf
	}
	return h
}

func (h *radixHeap) clear() {
	for i := range h.buckets {
		h.buckets[i] = h.buckets[i][:0]
		h.mins[i] = radixInf
	}
	h.size = 0
	h.last = 0
}

func (h *radixHeap) len() int { return h.size }

func msb(x uint64) int {
	b := 0
	for x >= 1<<8 {
		x >>= 8
		b += 8
	}
	for x >= 1<<4 {
		x >>= 4
		b += 4
	}
	for x > 0 {
		x >>= 1
		b++
	}
	return b
}

func (h *radixHeap) push(key uint64, node int) {
	b := msb(key ^ h.last)
	h.buckets[b] = append(h.buckets[b], rhEntry{key: key, node: node})
	if key < h.mins[b] {
		h.mins[b] = key
	}
	h.size++
}

func (h *radixHeap) pop() (uint64, int) {
	if len(h.buckets[0]) == 0 {
		i := 1
		for len(h.buckets[i]) == 0 {
			i++
		}
		h.last = h.mins[i]
		for _, e := range h.buckets[i] {
			b := msb(e.key ^ h.last)
			h.buckets[b] = append(h.buckets[b], e)
			if e.key < h.mins[b] {
				h.mins[b] = e.key
			}
		}
		h.buckets[i] = h.buckets[i][:0]
		h.mins[i] = radixInf
	}
	e := h.buckets[0][len(h.buckets[0])-1]
	h.buckets[0] = h.buckets[0][:len(h.buckets[0])-1]
	h.size--
	return e.key, e.node
}
