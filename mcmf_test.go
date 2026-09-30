package grantalloc

import "testing"

func TestMCMFBasic(t *testing.T) {
	g := newMinCostFlow(4)
	g.addEdge(0, 1, 2, 1)
	g.addEdge(0, 2, 2, 5)
	g.addEdge(1, 2, 1, 1)
	g.addEdge(1, 3, 1, 10)
	g.addEdge(2, 3, 2, 1)
	fl, cost := g.flow(0, 3, 3)
	if fl != 3 {
		t.Fatalf("flow = %d, want 3", fl)
	}
	// paths: 0-1-2-3=3 (cap1), 0-2-3=6, 0-1-3=11. Edge 2->3 cap2 is
	// shared, so 3 cheapest units cost 3 + 6 + 11 = 20.
	if cost != 20 {
		t.Fatalf("cost = %d, want 15", cost)
	}
}

func TestMCMFNegativeCost(t *testing.T) {
	g := newMinCostFlow(4)
	g.addEdge(0, 1, 3, -2)
	g.addEdge(0, 2, 3, 1)
	g.addEdge(1, 3, 2, -1)
	g.addEdge(2, 1, 2, 0)
	g.addEdge(2, 3, 3, -5)
	fl, cost := g.flow(0, 3, 4)
	if fl != 4 {
		t.Fatalf("flow = %d, want 4", fl)
	}
	if cost != -15 {
		t.Fatalf("cost = %d, want -18", cost)
	}
}

func TestMCMFInsufficient(t *testing.T) {
	g := newMinCostFlow(5)
	g.addEdge(0, 1, 2, 1)
	g.addEdge(1, 2, 1, 1)
	g.addEdge(2, 3, 3, 1)
	g.addEdge(0, 4, 1, 2)
	g.addEdge(4, 3, 1, 2)
	fl, _ := g.flow(0, 3, 5)
	if fl != 2 {
		t.Fatalf("flow = %d, want 2", fl)
	}
}

func TestMCMFEdgeFlow(t *testing.T) {
	g := newMinCostFlow(3)
	from, idx := g.addEdge(0, 1, 5, 1)
	g.addEdge(1, 2, 5, 1)
	fl, _ := g.flow(0, 2, 3)
	if fl != 3 || g.edgeFlow(from, idx) != 3 {
		t.Fatalf("flow=%d edgeFlow=%d", fl, g.edgeFlow(from, idx))
	}
}
