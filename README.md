# task26092924 — grantalloc

拨款支出整批分摊 Go 库（整数分、万分比、内存账本，无第三方依赖）。

- 调用说明与 API：`docs/USAGE.md`
- 验收场景测试：`ledger_test.go`（`TestScenario1/2/3`）
- floor/ceil 与资助限制：`rules_test.go`
- 穷举最优性对照（2000 组小规模随机）：`brute_test.go`
- 满规模耗时/内存/守恒：`fullscale_test.go`、`stress_test.go`

```sh
go test ./...          # 全部（含满规模，<1s，峰值约 4 MiB）
go test -short ./...   # 跳过满规模
```
