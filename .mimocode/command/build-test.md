---
description: "构建并测试 Orka Go 模块。支持指定单个模块或全部模块，可选 vet 和 test。"
---

对项目执行 Go 构建和测试。

## 使用方式
- `build-test` — 构建并测试所有模块
- `build-test control` — 仅构建并测试 orka_control_layer
- `build-test tools` — 仅构建并测试 tools_server
- `build-test core` — 仅构建并测试 orka_core
- `build-test middleware` — 仅构建并测试 orka_middleware
- `build-test web` — 构建 web 前端（npm run build）

## 执行步骤

1. 确定要构建的模块（从 $ARGUMENTS 解析，默认全部）
2. 执行构建：
   - 全部: `go build ./orka_core/... ./orka_middleware/... ./tools_server/... ./orka_control_layer/...`
   - 单模块: `go build ./<module>/...`
   - Web: `cd web && npm run build`
3. 执行 vet（仅 Go）: `go vet ./<module>/...`
4. 执行测试（仅 Go）: `go test ./<module>/... 2>&1 | grep -E 'ok|FAIL|panic'`
5. 输出汇总：通过/失败的包数量，如有失败则展示失败详情

## 注意事项
- 工作目录始终为 `/Users/shiyi/cavis`
- Go 多模块项目必须使用显式模块前缀，不能用 `go build ./...`
- 测试失败时不要自动修复，只报告结果

$ARGUMENTS
