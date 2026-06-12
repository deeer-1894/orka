---
description: "对 Orka 后端 API 执行端到端测试：登录、创建对话、发送消息、查看结果。"
---

对 Orka 后端 API 执行端到端测试流程。

## 使用方式
- `test-api` — 执行完整 E2E 测试（登录 → 创建对话 → 发送消息）
- `test-api login` — 仅测试登录
- `test-api chat <消息>` — 登录后发送指定消息并查看 SSE 响应

## 执行步骤

### 1. 登录获取 Token
```bash
B=http://127.0.0.1:8088/api/v1/controller
TOK=$(curl -s -m10 -X POST $B/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"real@test.com","password":"123456"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["token"])')
echo "Token: ${TOK:0:20}..."
```

### 2. 创建对话
```bash
CID=$(curl -s -m10 -X POST $B/conversation/create-conversation \
  -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' \
  -d '{}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["conversation_id"])')
echo "Conversation: $CID"
```

### 3. 发送消息（SSE 流式）
```bash
MSG="${ARGUMENTS:-你好，请介绍一下你自己}"
curl -sN -m 60 -X POST $B/chat/run \
  -H "Authorization: Bearer $TOK" \
  -H 'Content-Type: application/json' \
  -d "{\"message\":\"$MSG\",\"conversation_id\":\"$CID\"}" \
  | head -50
```

### 4. 输出结果
- 登录状态
- 对话 ID
- 消息响应（截取前 50 行）

## 前置条件
- orka_control 运行在 8088 端口
- 测试账号 `real@test.com` / `123456789` 已注册

$ARGUMENTS
