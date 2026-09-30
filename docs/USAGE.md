# grantalloc — 拨款支出整批分摊库

纯 Go、无第三方依赖、无外部服务。所有金额单位为**整数分**，资助比例用**万分比整数**表示。

## 何时使用

同一项资产由多项拨款按固定比例共同资助。逐笔四舍五入会导致尾差、逐笔补差可能提前耗尽某项拨款。
本库对**整批支出**统一求解，使：

- 每笔支出在其资助拨款之间全部分摊，每项实际值只能是
  `floor(金额×比例/10000)` 或 `ceil(...)`；
- 每项拨款在本批的实际总额只能是其全部理论份额之和的 floor 或 ceil，且不超过当前余额；
- 未参与该资产资助的拨款绝不承接支出；
- 在所有合法方案中，选择逐笔“实际值−理论值”绝对偏差之和**最小**的方案（按精确值比较）；
- 不存在合法方案时返回 `ErrInfeasible`，**绝不**改比例、丢支出或部分记账。

## 快速开始

```go
ledger, _ := grantalloc.NewLedger(
    map[string]int64{"A": 1, "B": 1},
    []grantalloc.AssetDef{
        {AssetID: "X", Fundings: []grantalloc.Funding{
            {GrantID: "A", Ratio: 6000},
            {GrantID: "B", Ratio: 4000},
        }},
    })

batch := []grantalloc.Expenditure{{LineID: "L1", Asset: "X", Amount: 1}}

// 试算：只读，不改余额、不写历史，可反复调用
plan, err := ledger.Trial("t-1", batch)

// 提交：按当时余额重新求解；成功后原子扣减并追加历史
res, err := ledger.Commit("c-1", batch)
```

## API

| 签名 | 说明 |
| --- | --- |
| `NewLedger(balances map[string]int64, assets []AssetDef) (*Ledger, error)` | 初始余额（非负）与资产资助关系（正比例之和恰好 10000）。 |
| `(*Ledger).Trial(batchID string, lines []Expenditure) (*BatchResult, error)` | 整批试算，不改变任何状态。 |
| `(*Ledger).Commit(batchID string, lines []Expenditure) (*BatchResult, error)` | 整批提交：重新求解、原子扣减余额、写入历史。批次号不可重复。 |
| `(*Ledger).Balance(id string) int64` / `Balances() map[string]int64` | 查询当前余额。 |
| `(*Ledger).History() []CommittedBatch` | 已提交批次（深拷贝），含逐笔明细与拨款汇总。 |

错误：

- 输入不合法（空批次号、重复行号、未知资产、比例不合规等）返回 `*ValidationError`；
- 余额/资助关系约束下无合法方案返回包级哨兵 `ErrInfeasible`，用 `errors.Is` 判断；
  返回该错误时余额与历史与调用前完全一致。

`BatchResult`：

- `Lines[i].Allocations`：该笔支出对各资助拨款的独立记账金额（合计恰等于支出金额）；
- `GrantTotals`：本批每项拨款实际分摊总额；
- `TotalDeviation`：所有逐笔分摊项绝对偏差之和（分，浮点仅用于展示，比较在内部按整数精确完成）。

## 算法

把“一笔支出中某个分摊项向上取整”看作一个单位流量：

- 源点 → 支出行，容量 = 该行需要向上取整的项数（0…k−1，k≤4）；
- 支出行 → 拨款，边权 = `1 − 小数部分`，即该项上调的绝对偏差；
- 拨款 → 汇点拆成“必选槽”和“至多一个可选槽”，容量受 `余额 − floor 总额` 限制。

必选槽给到大负权（字典序惩罚），保证 min-cost flow 优先填满每项拨款合法总额所必需的上调次数；
其余边权之和即逐笔偏差总和。求解采用带势能的逐次最短路 + radix-heap Dijkstra，
流不可行或必选槽未填满即判定整批不可分摊。

满规模（2000 笔 / 128 拨款 / 每资产≤4 拨款 / 单笔≤10 亿分）在普通笔记本上
试算约 0.2–0.4 秒、峰值堆内存约 4 MiB（见满规模测试日志）。

## 运行测试

```sh
go test ./...                 # 全部测试（含满规模性能与守恒检查）
go test -short ./...          # 跳过满规模用例
go test -race ./...           # 竞态检测
go test -v -run TestScenario  # 仅运行三个验收场景
go test ./... -bench .        # 基准测试（如已添加）
```

注意：账本设计为同一报表期内**串行**调用，不提供并发安全与持久化。
