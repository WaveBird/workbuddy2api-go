# 出站转储（腾讯视角）

> 这是本 fork 相对上游快照**唯一**的功能性改动：把 workbuddy2api 真正发往腾讯
> （`copilot.tencent.com`）的那一跳记到本地 JSONL，供 WorkBuddy Manager 的
> 「腾讯视角」回放。默认关闭，不改任何转发行为。

## 为什么需要

管理端只能看到两跳：

```
客户端 ──①──▶ manager ──②──▶ workbuddy2api ──③──▶ copilot.tencent.com
             ✅              ✅                   ❌ 本来是黑的
```

③ 这一段做的事恰恰是排障的关键现场，全在 workbuddy2api 进程里：

- 剥 `cn:` / `global:` 前缀、`rewriteModel` 改写模型名；
- `prompt.Rewrite` / `prompt.Append` 重写或追加 system 提示词（降级期还换成中性提示词）；
- 注入会话头族 `X-Conversation-ID` / `X-Conversation-Request-ID` / messageID；
- 风控头、UA（`CLI/2.63.2 CodeBuddy/2.63.2`）、账号 access token；
- 大请求轮转、gzip 压缩、换路径重试（`/v2` ↔ `/console`）。

「为什么被判 11-128」「为什么这个号拿不到这个模型」，答案都在这里，而管理端
一个字段都看不到。

## 怎么开

`config.json`：

```json
{
  "outbound_trace": {
    "enabled": true,
    "path": "./data/outbound-trace.jsonl",
    "max_body": 262144
  }
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `enabled` | `false` | 总开关。**默认关闭**：内容含完整对话（凭据已打码） |
| `path` | `./data/outbound-trace.jsonl` | 输出文件；目录会自动创建 |
| `max_body` | `262144` | 单条请求体 / 响应体的截断上限（字节） |

## 写出什么

每次调用最多两行 JSON（用 `trace_id` 关联）：

```json
{"kind":"request","trace_id":"…","uid":"…","ts":1794145000123,"method":"POST",
 "url":"https://copilot.tencent.com/v2/chat/completions",
 "headers":{"Authorization":"Bearer eyJhbG***","X-User-Id":"…","X-Conversation-ID":"…"},
 "body":"{\"model\":\"deepseek-v4.1-flash\",\"messages\":[…]}","truncated":false}
{"kind":"response","trace_id":"…","uid":"…","ts":1794145002456,"ms":2333,"status":200,
 "headers":{"Content-Type":"text/event-stream"},"body":"data: {...}\n\ndata: [DONE]\n",
 "truncated":false}
```

- `trace_id` 来自入站请求头 `X-Trace-ID`（管理端每条请求带一个），没带时按既有
  约定回落 —— 管理端据此把「它发出去的」与「上游发往腾讯的」精确对上，不靠时间窗猜；
- 响应正文对**流式**同样留存（前 `max_body` 字节），`truncated` 标记是否截断；
- `Authorization` / `Cookie` / `X-API-Key` / `X-Refresh-Token` 一律打码，规则与
  管理端一致（保留 scheme + 凭证前 6 位），两边前缀能对上才谈得上对照。

## 三条约束

1. **默认关闭**、开启后**只写文件**：转发路径上不加任何分支语义（`rec` 为 nil 时
   全是空操作）；
2. **不影响请求**：写文件失败只打一行 `WARN`，不改变请求或响应；
3. **有界**：单条截断到 `max_body`，一次调用最多两行，不会因为开了它把磁盘写满。

## 兼容性

纯增量：新增一个配置段、一个新文件、一个入站头（`X-Trace-ID`，缺省不影响任何
行为）。不开启时行为与上游快照完全一致 —— 现有部署不改配置即可原样升级。

## 来源与许可

本目录是上游 `workbuddy2api` 的源码快照（原仓库 `github.com/Sliverkiss/workbuddy2api`
已被作者删除，源码由 WorkBuddy Manager 维护者留存并继续维护），随该项目 Release
以 `upstream-src` 分发。许可为 **MIT**，版权归原作者（见 `LICENSE`）；本 fork 仅在其
之上增加上述出站转储能力。
