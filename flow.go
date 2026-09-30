package apportion

import "container/heap"

// edge 是最小费用流网络中的一条边。
type edge struct {
	to, rev int   // 终点与反向边在 g[to] 中的下标
	cap     int64 // 剩余容量
	cost    int64 // 单位费用（可为负）
}

// mcfGraph 为带势（Johnson 约化费用）的连续最短路最小费用流网络。
type mcfGraph struct {
	g [][]edge
}

// newMCFGraph 创建 n 个节点的空网络。
func newMCFGraph(n int) *mcfGraph { return &mcfGraph{g: make([][]edge, n)} }

// addEdge 加入一条容量 cap、单位费用 cost 的边，返回边在 g[from] 中的下标。
func (m *mcfGraph) addEdge(from, to int, cap, cost int64) int {
	m.g[from] = append(m.g[from], edge{to: to, rev: len(m.g[to]), cap: cap, cost: cost})
	m.g[to] = append(m.g[to], edge{to: from, rev: len(m.g[from]) - 1, cap: 0, cost: -cost})
	return len(m.g[from]) - 1
}

// flowOn 返回从 from 出发、加入时为第 idx 条边的当前流量。
func (m *mcfGraph) flowOn(from, idx int) int64 {
	e := m.g[from][idx]
	return m.g[e.to][e.rev].cap
}

// minCostMaxFlow 求 s->t 的最小费用最大流，返回 (流量, 费用)。
// 先用 Bellman-Ford 求初始势以处理负费用边，之后每轮用 Dijkstra 增广。
func (m *mcfGraph) minCostMaxFlow(s, t int) (flow, cost int64) {
	n := len(m.g)
	const inf = int64(1) << 62
	pot := make([]int64, n)
	for i := range pot {
		pot[i] = inf
	}
	pot[s] = 0
	// Bellman-Ford：初始势，使所有约化费用非负。
	for updated := true; updated; {
		updated = false
		for v := 0; v < n; v++ {
			if pot[v] == inf {
				continue
			}
			for _, e := range m.g[v] {
				if e.cap > 0 && pot[e.to] > pot[v]+e.cost {
					pot[e.to] = pot[v] + e.cost
					updated = true
				}
			}
		}
	}
	dist := make([]int64, n)
	prevV := make([]int, n)
	prevE := make([]int, n)
	for {
		for i := range dist {
			dist[i] = inf
		}
		dist[s] = 0
		pq := &nodeHeap{{v: s, d: 0}}
		for pq.Len() > 0 {
			cur := heap.Pop(pq).(nodeItem)
			if cur.d > dist[cur.v] {
				continue
			}
			for i, e := range m.g[cur.v] {
				if e.cap <= 0 {
					continue
				}
				nd := cur.d + e.cost + pot[cur.v] - pot[e.to]
				if nd < dist[e.to] {
					dist[e.to] = nd
					prevV[e.to] = cur.v
					prevE[e.to] = i
					heap.Push(pq, nodeItem{v: e.to, d: nd})
				}
			}
		}
		if dist[t] == inf {
			return flow, cost
		}
		for v := 0; v < n; v++ {
			if dist[v] < inf {
				pot[v] += dist[v]
			}
		}
		// 本次增广的流量：路径上的最小剩余容量。
		d := int64(inf)
		for v := t; v != s; v = prevV[v] {
			if c := m.g[prevV[v]][prevE[v]].cap; c < d {
				d = c
			}
		}
		flow += d
		cost += d * pot[t] // pot[t] 即 s->t 最短路的原始费用
		for v := t; v != s; v = prevV[v] {
			e := &m.g[prevV[v]][prevE[v]]
			e.cap -= d
			m.g[v][e.rev].cap += d
		}
	}
}

type nodeItem struct {
	v int
	d int64
}
type nodeHeap []nodeItem

func (h nodeHeap) Len() int            { return len(h) }
func (h nodeHeap) Less(i, j int) bool  { return h[i].d < h[j].d }
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(nodeItem)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
